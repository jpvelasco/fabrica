package aws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.EC2InstanceManager = (*ec2Service)(nil)

// ec2Service owns the provider's lazily initialized EC2 SDK client. Both
// instance lifecycle actions and AMI resolution share this single client.
type ec2Service struct {
	awsCfg awsConfig
	client ec2APIClient

	// initMu serializes lazy initialization (concurrent MCP tool calls share
	// this service). Not held during API calls.
	initMu sync.Mutex

	// seams for testing — nil means use real SDK
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ec2APIClient
}

// ec2APIClient is the subset of the EC2 SDK client surface used by ec2Service.
type ec2APIClient interface {
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeVolumes(ctx context.Context, params *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeSecurityGroupRules(ctx context.Context, params *ec2.DescribeSecurityGroupRulesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupRulesOutput, error)
}

func (s *ec2Service) ensureClient(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	if s.client != nil {
		return nil
	}
	loadCfg := s.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, s.awsCfg.region, s.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for EC2 service: %w", err)
	}
	newClient := s.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ec2APIClient {
			return ec2.NewFromConfig(cfg)
		}
	}
	s.client = newClient(cfg)
	return nil
}

// RunInstances launches one instance. resourceClients uses it when Cloud
// Control cannot tag the root volume on the create request.
func (s *ec2Service) RunInstances(ctx context.Context, in *ec2.RunInstancesInput) (*ec2.RunInstancesOutput, error) {
	if err := s.ensureClient(ctx); err != nil {
		return nil, err
	}
	return s.client.RunInstances(ctx, in)
}

// DescribeInstances queries EC2 instances by tag or state filter. The
// RunInstances fallback path uses it to adopt a same-nonce instance a
// previous attempt already launched, and to surface a leftover from a
// previous run — which fails the create instead of launching a second one.
func (s *ec2Service) DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	if err := s.ensureClient(ctx); err != nil {
		return nil, err
	}
	return s.client.DescribeInstances(ctx, in)
}

// TagInstanceVolumes applies tags to every EBS volume attached to the
// instance. Cloud Control cannot tag BlockDeviceMapping volumes at creation,
// so create flows call this right after the instance step; without it, data
// volumes are invisible to ManagedBy sweeps (road test D8).
func (s *ec2Service) TagInstanceVolumes(ctx context.Context, instanceID string, tags map[string]string) error {
	if err := s.ensureClient(ctx); err != nil {
		return err
	}
	if len(tags) == 0 {
		return nil
	}

	ec2Tags := make([]types.Tag, 0, len(tags))
	for k, v := range tags {
		ec2Tags = append(ec2Tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	// Volumes attach asynchronously right after launch — retry briefly when
	// none are visible yet so a fast poll doesn't miss them.
	const attempts = 5
	var vols []types.Volume
	for i := 0; i < attempts; i++ {
		out, err := s.client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
			Filters: []types.Filter{
				{Name: aws.String("attachment.instance-id"), Values: []string{instanceID}},
			},
		})
		if err != nil {
			return fmt.Errorf("describing volumes attached to %s: %w", instanceID, err)
		}
		if len(out.Volumes) > 0 || i == attempts-1 {
			vols = out.Volumes
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for volumes on %s: %w", instanceID, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}

	for _, v := range vols {
		if _, err := s.client.CreateTags(ctx, &ec2.CreateTagsInput{
			Resources: []string{aws.ToString(v.VolumeId)},
			Tags:      ec2Tags,
		}); err != nil {
			return fmt.Errorf("tagging volume %s: %w", aws.ToString(v.VolumeId), err)
		}
	}
	return nil
}

// sgRuleSpec is one ingress rule from a desired-state doc, used to match the
// live SecurityGroupRuleId after creation. Blank fields match any live value:
// the doc only carries the fields the plan sets.
type sgRuleSpec struct {
	IpProtocol            string
	FromPort, ToPort      *int
	CidrIp                string
	Ipv6CidrIp            string
	PrefixListId          string
	SourceSecurityGroupId string
}

// TagSecurityGroupRules applies tags to the ingress rules of a security
// group whose rules match the given specs. Cloud Control cannot tag
// SecurityGroupRule resources, so create flows call this right after the
// group exists; without it, rules are invisible to ManagedBy sweeps. It
// returns the number of rules tagged. Zero specs or zero tags is a no-op.
func (s *ec2Service) TagSecurityGroupRules(ctx context.Context, groupID string, specs []sgRuleSpec, tags map[string]string) (int, error) {
	if len(specs) == 0 || len(tags) == 0 {
		return 0, nil
	}
	if err := s.ensureClient(ctx); err != nil {
		return 0, err
	}

	ec2Tags := make([]types.Tag, 0, len(tags))
	for k, v := range tags {
		ec2Tags = append(ec2Tags, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	var ruleIDs []string
	var nextToken *string
	for {
		out, err := s.client.DescribeSecurityGroupRules(ctx, &ec2.DescribeSecurityGroupRulesInput{
			Filters:   []types.Filter{{Name: aws.String("group-id"), Values: []string{groupID}}},
			NextToken: nextToken,
		})
		if err != nil {
			return 0, fmt.Errorf("describing security group rules for %s: %w", groupID, err)
		}
		for _, r := range out.SecurityGroupRules {
			// Only ingress rules: the doc carries SecurityGroupIngress, and
			// tagging an egress rule the doc never declared would be wrong.
			if aws.ToBool(r.IsEgress) {
				continue
			}
			if sgRuleMatches(r, specs) {
				ruleIDs = append(ruleIDs, aws.ToString(r.SecurityGroupRuleId))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	if len(ruleIDs) == 0 {
		return 0, nil
	}
	if _, err := s.client.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: ruleIDs,
		Tags:      ec2Tags,
	}); err != nil {
		return 0, fmt.Errorf("tagging security group rules on %s: %w", groupID, err)
	}
	return len(ruleIDs), nil
}

// sgRuleMatches reports whether a live ingress rule matches any spec: every
// field the spec carries must equal the live value. Source-SG rules match on
// the referenced group ID, which is how standalone
// AWS::EC2::SecurityGroupIngress resources identify their source.
func sgRuleMatches(r types.SecurityGroupRule, specs []sgRuleSpec) bool {
	for _, spec := range specs {
		if spec.IpProtocol != "" && aws.ToString(r.IpProtocol) != spec.IpProtocol {
			continue
		}
		if spec.FromPort != nil && (r.FromPort == nil || int(*r.FromPort) != *spec.FromPort) {
			continue
		}
		if spec.ToPort != nil && (r.ToPort == nil || int(*r.ToPort) != *spec.ToPort) {
			continue
		}
		if spec.CidrIp != "" && aws.ToString(r.CidrIpv4) != spec.CidrIp {
			continue
		}
		if spec.Ipv6CidrIp != "" && aws.ToString(r.CidrIpv6) != spec.Ipv6CidrIp {
			continue
		}
		if spec.PrefixListId != "" && aws.ToString(r.PrefixListId) != spec.PrefixListId {
			continue
		}
		if spec.SourceSecurityGroupId != "" {
			ref := r.ReferencedGroupInfo
			if ref == nil || ref.GroupId == nil || aws.ToString(ref.GroupId) != spec.SourceSecurityGroupId {
				continue
			}
		}
		return true
	}
	return false
}

// sgIngressSpecs extracts the match specs for a security group doc's inline
// "SecurityGroupIngress" rules.
func sgIngressSpecs(doc map[string]any) []sgRuleSpec {
	v, ok := doc["SecurityGroupIngress"].([]any)
	if !ok {
		return nil
	}
	specs := make([]sgRuleSpec, 0, len(v))
	for _, entry := range v {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		specs = append(specs, sgRuleSpecFromDoc(m))
	}
	return specs
}

// sgRuleSpecFromDoc builds a match spec from one ingress rule's doc entry
// (inline or standalone). Absent fields stay blank and match any live value.
func sgRuleSpecFromDoc(m map[string]any) sgRuleSpec {
	spec := sgRuleSpec{
		IpProtocol:            docString(m, "IpProtocol"),
		CidrIp:                docString(m, "CidrIp"),
		Ipv6CidrIp:            docString(m, "Ipv6CidrIp"),
		PrefixListId:          docString(m, "PrefixListId"),
		SourceSecurityGroupId: docString(m, "SourceSecurityGroupId"),
	}
	if p := docInt(m, "FromPort"); p != nil {
		spec.FromPort = p
	}
	if p := docInt(m, "ToPort"); p != nil {
		spec.ToPort = p
	}
	return spec
}

func docString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// docInt reads a numeric field from an unmarshalled desired-state doc (JSON
// numbers arrive as float64).
func docInt(m map[string]any, key string) *int {
	switch v := m[key].(type) {
	case float64:
		i := int(v)
		return &i
	case int:
		return &v
	}
	return nil
}

// StopInstance stops the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach stopped state).
func (s *ec2Service) StopInstance(ctx context.Context, instanceID string) error {
	if err := s.ensureClient(ctx); err != nil {
		return err
	}
	_, err := s.client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("stopping instance %s: %w", instanceID, err)
	}
	return nil
}

// StartInstance starts the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach running state).
func (s *ec2Service) StartInstance(ctx context.Context, instanceID string) error {
	if err := s.ensureClient(ctx); err != nil {
		return err
	}
	_, err := s.client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("starting instance %s: %w", instanceID, err)
	}
	return nil
}
