package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// fallbackVolumeTags retries an EC2 instance create once through RunInstances
// when Cloud Control was denied for a missing volume request tag. Any other
// error is returned unchanged. A successful fallback replaces the error.
func (c *resourceClients) fallbackVolumeTags(ctx context.Context, r *fabricac.Resource, err error) error {
	if err == nil || r == nil || r.TypeName != fabricac.TypeAWSEC2Instance || !isVolumeTagDeny(err) {
		return err
	}
	if fbErr := c.createInstanceWithVolumeTags(ctx, r); fbErr != nil {
		return fmt.Errorf("%w; RunInstances fallback failed: %v", err, fbErr)
	}
	return nil
}

// isVolumeTagDeny reports a permission or request-tag failure. Cloud Control
// can tag the instance and still omit the tag on the auto-created root volume.
// Matching is case-insensitive and does not require both the action name and
// the word "volume": accounts phrase the same deny in several ways.
func isVolumeTagDeny(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"unauthorizedoperation",
		"accessdenied",
		"not authorized",
		"explicit deny",
		"requesttag",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// createInstanceWithVolumeTags launches the instance with TagSpecifications
// for both the instance and the volume, and IMDSv2 required.
func (c *resourceClients) createInstanceWithVolumeTags(ctx context.Context, r *fabricac.Resource) error {
	input, err := runInstancesInput(r.DesiredState, c.volumeRequestTags)
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

func runInstancesInput(desired json.RawMessage, volumeTags map[string]string) (*ec2.RunInstancesInput, error) {
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
