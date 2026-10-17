package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

// fallbackVolumeTags retries an EC2 instance create once through direct
// RunInstances, only on a volume-tag deny. Any other error is returned
// unchanged: waiter and polling errors never reach this path (the create path
// returns them to the operator), and a failure that already names an
// identifier never reaches it (failedInstanceCreate gates on an empty
// identifier) — in both cases a retry would risk launching a second instance.
// A successful fallback replaces the error.
func (c *resourceClients) fallbackVolumeTags(ctx context.Context, r *fabricac.Resource, err error) error {
	if err == nil || r == nil || r.TypeName != fabricac.TypeAWSEC2Instance || !isVolumeTagDeny(err) {
		return err
	}
	if fbErr := c.createInstanceWithVolumeTags(ctx, r); fbErr != nil {
		return fmt.Errorf("%w; RunInstances fallback failed: %v", err, fbErr)
	}
	return nil
}

// isVolumeTagDeny reports a volume-tag deny in the error text (matched without
// case sensitivity): the deny code (AccessDenied or UnauthorizedOperation),
// the EC2 action (ec2:RunInstances or ec2:CreateTags), and a volume or
// aws:RequestTag condition. The volume is matched on the EC2 volume resource
// ARN shape (arn:aws:ec2:*:*:volume/...), not on the bare word "volume", so a
// deny that merely mentions volumes in prose cannot trigger the retry. Any
// other deny — iam:PassRole, KMS, SSM, a path or boundary deny, throttling, a
// bad AMI — does not match: the retry cannot fix the underlying permission,
// and the launch would risk a second instance.
func isVolumeTagDeny(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "accessdenied") && !strings.Contains(msg, "unauthorizedoperation") {
		return false
	}
	if !strings.Contains(msg, "ec2:runinstances") && !strings.Contains(msg, "ec2:createtags") {
		return false
	}
	return strings.Contains(msg, "requesttag") || strings.Contains(msg, volumeResourceARNNeedle)
}

// volumeResourceARNNeedle is the lowercased marker of an EC2 volume resource
// ARN (arn:aws:ec2:region:account:volume/vol-...); the region and account
// segments vary, so the match stops at the ":volume/" boundary.
const volumeResourceARNNeedle = ":volume/"

// createInstanceWithVolumeTags launches the instance with TagSpecifications
// for both the instance and the volume, and IMDSv2 required. Before launching
// it looks for the instance this create already launched and adopts it
// instead of launching a second one. The request carries a per-create
// ClientToken so a repeated attempt is an idempotent retry.
func (c *resourceClients) createInstanceWithVolumeTags(ctx context.Context, r *fabricac.Resource) error {
	id, err := c.adoptFabricaInstance(ctx, r)
	if err != nil {
		return err
	}
	if id != "" {
		r.Identifier = id
		return nil
	}
	input, err := runInstancesInput(r.DesiredState, c.volumeRequestTags, c.runInstancesClientToken(r))
	if err != nil {
		return err
	}
	var out *ec2.RunInstancesOutput
	switch {
	case c.runInstances != nil:
		out, err = c.runInstances(ctx, input)
	case c.instances != nil:
		out, err = c.instances.RunInstances(ctx, input)
	default:
		err = fmt.Errorf("EC2 client is not configured")
	}
	if err != nil {
		return fmt.Errorf("creating %s via RunInstances: %w", r.TypeName, err)
	}
	if out == nil || len(out.Instances) == 0 || out.Instances[0].InstanceId == nil || aws.ToString(out.Instances[0].InstanceId) == "" {
		return fmt.Errorf("creating %s via RunInstances: API returned no instance ID", r.TypeName)
	}
	r.Identifier = aws.ToString(out.Instances[0].InstanceId)
	return nil
}

// docTagValue returns the value of the named tag in the desired state.
// Empty when the document has no such tag or does not parse.
func docTagValue(desired json.RawMessage, key string) string {
	var doc ccInstanceDoc
	if err := json.Unmarshal(desired, &doc); err != nil {
		return ""
	}
	for _, tag := range doc.Tags {
		if tag.Key == key {
			return tag.Value
		}
	}
	return ""
}

// nameTag returns the value of the Name tag in the desired state — the
// identity the RunInstances path uses for adoption and the idempotency token.
func nameTag(desired json.RawMessage) string {
	return docTagValue(desired, "Name")
}

// createNonceTag returns the value of the FabricaCreateNonce tag in the
// desired state — the per-create identity the RunInstances path matches on
// during adoption and seeds into the idempotency token.
func createNonceTag(desired json.RawMessage) string {
	return docTagValue(desired, fabricac.CreateNonceTag)
}

// runInstancesClientToken derives the idempotency token from the resource
// identity: type, name tag, region, and the fabrica version, plus the
// per-create nonce tag when present. Retries of the same create send the same
// token, so EC2 returns the original instance instead of a second one; a
// re-create after terminate carries a fresh nonce and therefore a fresh
// token. The sha256 hex digest is exactly 64 characters, the RunInstances
// ClientToken limit.
func (c *resourceClients) runInstancesClientToken(r *fabricac.Resource) string {
	seed := fmt.Sprintf("%s|%s|%s|%s", r.TypeName, nameTag(r.DesiredState), c.awsCfg.region, c.version)
	if nonce := createNonceTag(r.DesiredState); nonce != "" {
		seed += "|" + nonce
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// adoptFabricaInstance looks for the instance this create already launched:
// the desired state names it by its fabrica tags (ManagedBy=fabrica,
// Name=<name>, and the per-create FabricaCreateNonce when present) in
// pending or running state, so a retried create adopts it instead of
// launching a second instance. Adoption only accepts exactly one match; a
// match must also have IMDSv2 required and the instance profile the desired
// state names, or the create fails instead of adopting a foreign or
// misconfigured instance. Two or more matches are an error: the second
// instance cannot be launched and the extras need operator attention. A
// lookup error falls through to the launch, where the ClientToken keeps the
// attempt idempotent.
func (c *resourceClients) adoptFabricaInstance(ctx context.Context, r *fabricac.Resource) (string, error) {
	if c.instances == nil {
		return "", nil
	}
	name := nameTag(r.DesiredState)
	if name == "" {
		return "", nil
	}
	filters := []types.Filter{
		{Name: aws.String("tag:ManagedBy"), Values: []string{"fabrica"}},
		{Name: aws.String("tag:Name"), Values: []string{name}},
		{Name: aws.String("instance-state-name"), Values: []string{
			string(types.InstanceStateNamePending),
			string(types.InstanceStateNameRunning),
		}},
	}
	if nonce := createNonceTag(r.DesiredState); nonce != "" {
		filters = append(filters, types.Filter{Name: aws.String("tag:" + fabricac.CreateNonceTag), Values: []string{nonce}})
	}
	out, err := c.instances.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: filters})
	if err != nil {
		oplog.WithResourceAndRegion(r.TypeName, name, c.awsCfg.region).Warn("lookup of an existing fabrica instance failed; the ClientToken keeps the launch idempotent", "error", err)
		return "", nil
	}
	var matches []types.Instance
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if aws.ToString(inst.InstanceId) != "" {
				matches = append(matches, inst)
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		inst := matches[0]
		id := aws.ToString(inst.InstanceId)
		if err := verifyAdoptedInstance(inst, r.DesiredState); err != nil {
			return "", fmt.Errorf("adopting existing instance %s for %s: %w", id, name, err)
		}
		oplog.WithResourceAndRegion(r.TypeName, id, c.awsCfg.region).Info("adopting an existing fabrica instance", "name", name)
		return id, nil
	default:
		filters := "ManagedBy=fabrica and Name=" + name
		if nonce := createNonceTag(r.DesiredState); nonce != "" {
			filters += fmt.Sprintf(" and %s=%s", fabricac.CreateNonceTag, nonce)
		}
		return "", fmt.Errorf("adopting %s: found %d pending or running instances matching %s; only exactly one may match. Delete the extras, then re-run the create",
			name, len(matches), filters)
	}
}

// verifyAdoptedInstance checks the adoption safety invariants on a match:
// IMDSv2 must be required and the instance profile must be the one the
// desired state names. A match that fails either is a foreign or
// misconfigured instance, and adopting it would hand the create over to the
// wrong machine.
func verifyAdoptedInstance(inst types.Instance, desired json.RawMessage) error {
	if inst.MetadataOptions == nil || inst.MetadataOptions.HttpTokens != types.HttpTokensStateRequired {
		return fmt.Errorf("IMDSv2 is not required (MetadataOptions.HttpTokens = %q, want %q)",
			httpTokensString(inst.MetadataOptions), string(types.HttpTokensStateRequired))
	}
	if want := instanceProfileName(instanceProfileRaw(desired)); want != "" {
		got := instanceProfileNameFromARN(inst.IamInstanceProfile)
		if got != want {
			return fmt.Errorf("instance profile = %q, want %q", got, want)
		}
	}
	return nil
}

// instanceProfileNameFromARN extracts the profile name from the instance's
// profile ARN (arn:aws:iam::<account>:instance-profile/<name>). The EC2
// response carries only the ARN and ID — no name — so the suffix is the
// identity adoption compares against the desired state. Empty for an absent
// profile or an ARN without the expected shape.
func instanceProfileNameFromARN(p *types.IamInstanceProfile) string {
	if p == nil {
		return ""
	}
	arn := aws.ToString(p.Arn)
	const marker = "instance-profile/"
	if i := strings.LastIndex(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}
	return ""
}

// httpTokensString renders the IMDSv2 token mode of a match for the adoption
// error message; a nil metadata block is the "absent" case.
func httpTokensString(opts *types.InstanceMetadataOptionsResponse) string {
	if opts == nil {
		return "absent"
	}
	return string(opts.HttpTokens)
}

// instanceProfileRaw extracts the raw IamInstanceProfile value from the
// desired state so adoption can verify the profile name on the match.
func instanceProfileRaw(desired json.RawMessage) json.RawMessage {
	var doc ccInstanceDoc
	if err := json.Unmarshal(desired, &doc); err != nil {
		return nil
	}
	return doc.IamInstanceProfile
}

type ccInstanceDoc struct {
	ImageID             string          `json:"ImageId"`
	InstanceType        string          `json:"InstanceType"`
	SubnetID            string          `json:"SubnetId"`
	SecurityGroupIds    []string        `json:"SecurityGroupIds"`
	UserData            string          `json:"UserData"`
	IamInstanceProfile  json.RawMessage `json:"IamInstanceProfile"`
	BlockDeviceMappings []struct {
		DeviceName string `json:"DeviceName"`
		Ebs        struct {
			VolumeSize          int    `json:"VolumeSize"`
			VolumeType          string `json:"VolumeType"`
			DeleteOnTermination bool   `json:"DeleteOnTermination"`
		} `json:"Ebs"`
	} `json:"BlockDeviceMappings"`
	Tags []struct {
		Key   string `json:"Key"`
		Value string `json:"Value"`
	} `json:"Tags"`
	MetadataOptions struct {
		HttpPutResponseHopLimit int `json:"HttpPutResponseHopLimit"`
	} `json:"MetadataOptions"`
}

// The IMDSv2 hop limit is legal in 1..64. Capping at the service bound keeps
// the int32 conversion below (and the request itself) within range: an
// out-of-range desired-state value is malformed, so the fallback fails loudly
// instead of silently clamping.
const maxIMDSv2HopLimit = 64

func runInstancesInput(desired json.RawMessage, volumeTags map[string]string, clientToken string) (*ec2.RunInstancesInput, error) {
	var doc ccInstanceDoc
	if err := json.Unmarshal(desired, &doc); err != nil {
		return nil, fmt.Errorf("reading EC2 instance desired state: %w", err)
	}
	if doc.ImageID == "" || doc.InstanceType == "" {
		return nil, fmt.Errorf("EC2 instance desired state is missing ImageId or InstanceType")
	}

	// Capacity is seeded with the document's tag count alone; the volume tags
	// are appended and the slice grows on demand. Summing the two counts in a
	// make hint could overflow int for a malformed document (CodeQL
	// alloc-integer-overflow), so the hint stays a single len.
	instanceTags := make([]types.Tag, 0, len(doc.Tags))
	seen := map[string]struct{}{}
	for _, tag := range doc.Tags {
		if tag.Key == "" {
			continue
		}
		seen[tag.Key] = struct{}{}
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(tag.Key), Value: aws.String(tag.Value)})
	}
	for k, v := range volumeTags {
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		instanceTags = append(instanceTags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	sortTags(instanceTags)
	// The volume request carries the same tags as the instance. A guard that
	// requires Project on the volume still sees it, and ManagedBy rides along.
	vol := append([]types.Tag(nil), instanceTags...)

	hop, err := sanitizeIMDSv2HopLimit(doc.MetadataOptions.HttpPutResponseHopLimit)
	if err != nil {
		return nil, err
	}
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(doc.ImageID),
		InstanceType: types.InstanceType(doc.InstanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(int32(hop)), // #nosec G115 -- hop is clamped to 1..maxIMDSv2HopLimit by sanitizeIMDSv2HopLimit
		},
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: instanceTags},
			{ResourceType: types.ResourceTypeVolume, Tags: vol},
		},
	}
	if clientToken != "" {
		// A stable token keyed on the resource identity makes a repeated
		// RunInstances the same idempotent request: EC2 returns the original
		// instance instead of a second one when the token repeats.
		input.ClientToken = aws.String(clientToken)
	}
	if doc.UserData != "" {
		input.UserData = aws.String(doc.UserData)
	}
	if doc.SubnetID != "" {
		input.SubnetId = aws.String(doc.SubnetID)
	}
	if len(doc.SecurityGroupIds) > 0 {
		input.SecurityGroupIds = doc.SecurityGroupIds
	}
	if name := instanceProfileName(doc.IamInstanceProfile); name != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{Name: aws.String(name)}
	}
	if len(doc.BlockDeviceMappings) > 0 {
		input.BlockDeviceMappings = make([]types.BlockDeviceMapping, 0, len(doc.BlockDeviceMappings))
		for _, m := range doc.BlockDeviceMappings {
			ebs := &types.EbsBlockDevice{DeleteOnTermination: aws.Bool(m.Ebs.DeleteOnTermination)}
			if m.Ebs.VolumeSize > 0 {
				ebs.VolumeSize = aws.Int32(int32(m.Ebs.VolumeSize)) // #nosec G115 -- volume size is GiB, far below int32
			}
			if m.Ebs.VolumeType != "" {
				ebs.VolumeType = types.VolumeType(m.Ebs.VolumeType)
			}
			input.BlockDeviceMappings = append(input.BlockDeviceMappings, types.BlockDeviceMapping{
				DeviceName: aws.String(m.DeviceName),
				Ebs:        ebs,
			})
		}
	}
	return input, nil
}

// sanitizeIMDSv2HopLimit normalizes the desired-state hop limit for the
// RunInstances request. Absent (0) falls back to the pinned default of 1; a
// value above the service bound (64) is a malformed desired state and errors,
// because the caller's desired state cannot be honored as written.
func sanitizeIMDSv2HopLimit(hop int) (int, error) {
	if hop == 0 {
		return 1, nil
	}
	if hop < 1 || hop > maxIMDSv2HopLimit {
		return 0, fmt.Errorf("EC2 instance desired state has an invalid IMDSv2 hop limit %d; it must be between 1 and %d", hop, maxIMDSv2HopLimit)
	}
	return hop, nil
}

func instanceProfileName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return ""
		}
		return name
	}
	var obj struct {
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	return obj.Name
}

func sortTags(tags []types.Tag) {
	slices.SortFunc(tags, func(a, b types.Tag) int {
		return strings.Compare(aws.ToString(a.Key), aws.ToString(b.Key))
	})
}
