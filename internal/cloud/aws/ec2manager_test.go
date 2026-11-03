package aws

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// fakeEC2Client implements ec2APIClient for testing.
type fakeEC2Client struct {
	stubEC2
	stopErr     error
	startErr    error
	stopCalls   int
	startCalls  int
	lastStopIDs []string
	lastStartID []string
}

func (f *fakeEC2Client) StopInstances(_ context.Context, in *ec2.StopInstancesInput, _ ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	f.stopCalls++
	f.lastStopIDs = in.InstanceIds
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	return &ec2.StopInstancesOutput{}, nil
}

func (f *fakeEC2Client) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{}, nil
}

func (f *fakeEC2Client) CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

func (f *fakeEC2Client) StartInstances(_ context.Context, in *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalls++
	f.lastStartID = in.InstanceIds
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &ec2.StartInstancesOutput{}, nil
}

// fakeSGRuleEC2Client drives TagSecurityGroupRules: scripted
// DescribeSecurityGroupRules pages plus a CreateTags recorder.
type fakeSGRuleEC2Client struct {
	stubEC2
	pages       []ec2.DescribeSecurityGroupRulesOutput
	describeErr error
	createErr   error
	calls       int
	tagCalls    int
	lastRuleIDs []string
	lastTags    []types.Tag
}

func (f *fakeSGRuleEC2Client) DescribeSecurityGroupRules(context.Context, *ec2.DescribeSecurityGroupRulesInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupRulesOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	i := f.calls
	f.calls++
	if i >= len(f.pages) {
		return &ec2.DescribeSecurityGroupRulesOutput{}, nil
	}
	return &f.pages[i], nil
}

func (f *fakeSGRuleEC2Client) CreateTags(_ context.Context, in *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.tagCalls++
	f.lastRuleIDs = in.Resources
	f.lastTags = in.Tags
	return &ec2.CreateTagsOutput{}, nil
}

func newSGRuleTest(t *testing.T, fake *fakeSGRuleEC2Client) *ec2Service {
	t.Helper()
	return &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
		newClient: func(aws.Config) ec2APIClient { return fake },
	}
}

func liveIngressRule(id, protocol, cidr string, from, to *int32, egress bool, refSG string) types.SecurityGroupRule {
	r := types.SecurityGroupRule{
		SecurityGroupRuleId: aws.String(id),
		IpProtocol:          aws.String(protocol),
		FromPort:            from,
		ToPort:              to,
		CidrIpv4:            aws.String(cidr),
		IsEgress:            aws.Bool(egress),
	}
	if refSG != "" {
		r.ReferencedGroupInfo = &types.ReferencedSecurityGroup{GroupId: aws.String(refSG)}
	}
	return r
}

func TestTagSecurityGroupRulesMatchesIngressOnly(t *testing.T) {
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []types.SecurityGroupRule{
			liveIngressRule("sgr-1", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), false, ""),
			liveIngressRule("sgr-2", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), true, ""),
			liveIngressRule("sgr-3", "tcp", "10.0.0.0/8", aws.Int32(80), aws.Int32(80), false, ""),
		},
	}}}
	s := newSGRuleTest(t, fake)

	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{
		{IpProtocol: "tcp", FromPort: intPtr(5000), ToPort: intPtr(5000), CidrIp: "10.0.0.0/8"},
	}, map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 1 {
		t.Errorf("matched = %d, want 1", n)
	}
	if fake.tagCalls != 1 {
		t.Fatalf("CreateTags calls = %d, want 1", fake.tagCalls)
	}
	if len(fake.lastRuleIDs) != 1 || fake.lastRuleIDs[0] != "sgr-1" {
		t.Errorf("tagged rules = %v, want [sgr-1] (egress sgr-2 and port-mismatched sgr-3 excluded)", fake.lastRuleIDs)
	}
	if len(fake.lastTags) != 2 {
		t.Errorf("tags = %v, want 2 entries", fake.lastTags)
	}
}

func TestTagSecurityGroupRulesAllProtocolMatchesWithoutPorts(t *testing.T) {
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []types.SecurityGroupRule{
			{
				SecurityGroupRuleId: aws.String("sgr-all"),
				IpProtocol:          aws.String("-1"),
				CidrIpv4:            aws.String("10.0.0.0/8"),
				IsEgress:            aws.Bool(false),
			},
			liveIngressRule("sgr-tcp", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), false, ""),
		},
	}}}
	s := newSGRuleTest(t, fake)

	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{
		{IpProtocol: "-1"},
	}, map[string]string{"ManagedBy": "fabrica"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 1 {
		t.Errorf("matched = %d, want 1 (all-traffic rule, no ports to compare)", n)
	}
	if fake.tagCalls != 1 || len(fake.lastRuleIDs) != 1 || fake.lastRuleIDs[0] != "sgr-all" {
		t.Errorf("tagged rules = %v, want [sgr-all]", fake.lastRuleIDs)
	}
}

func TestTagSecurityGroupRulesPaginationBatchesOneCreateTags(t *testing.T) {
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{
		{
			SecurityGroupRules: []types.SecurityGroupRule{
				liveIngressRule("sgr-1", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), false, ""),
			},
			NextToken: aws.String("page-2"),
		},
		{
			SecurityGroupRules: []types.SecurityGroupRule{
				liveIngressRule("sgr-2", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), false, ""),
			},
		},
	}}
	s := newSGRuleTest(t, fake)

	spec := sgRuleSpec{IpProtocol: "tcp", FromPort: intPtr(5000), ToPort: intPtr(5000), CidrIp: "10.0.0.0/8"}
	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{spec}, map[string]string{"ManagedBy": "fabrica"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 2 {
		t.Errorf("matched = %d, want 2 (one per page)", n)
	}
	if fake.calls != 2 {
		t.Errorf("DescribeSecurityGroupRules calls = %d, want 2 (both pages)", fake.calls)
	}
	if fake.tagCalls != 1 {
		t.Errorf("CreateTags calls = %d, want 1 (one batched call)", fake.tagCalls)
	}
	if len(fake.lastRuleIDs) != 2 || fake.lastRuleIDs[0] != "sgr-1" || fake.lastRuleIDs[1] != "sgr-2" {
		t.Errorf("tagged rules = %v, want [sgr-1 sgr-2]", fake.lastRuleIDs)
	}
}

func TestTagSecurityGroupRulesSourceSGMatch(t *testing.T) {
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []types.SecurityGroupRule{
			liveIngressRule("sgr-from-agent", "tcp", "", aws.Int32(5000), aws.Int32(5000), false, "sg-agent456"),
			liveIngressRule("sgr-from-elsewhere", "tcp", "", aws.Int32(5000), aws.Int32(5000), false, "sg-other789"),
		},
	}}}
	s := newSGRuleTest(t, fake)

	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{
		{IpProtocol: "tcp", FromPort: intPtr(5000), ToPort: intPtr(5000), SourceSecurityGroupId: "sg-agent456"},
	}, map[string]string{"ManagedBy": "fabrica"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 1 {
		t.Errorf("matched = %d, want 1 (referenced source SG only)", n)
	}
	if fake.tagCalls != 1 || len(fake.lastRuleIDs) != 1 || fake.lastRuleIDs[0] != "sgr-from-agent" {
		t.Errorf("tagged rules = %v, want [sgr-from-agent]", fake.lastRuleIDs)
	}
}

func TestTagSecurityGroupRulesNoMatchIsNoop(t *testing.T) {
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []types.SecurityGroupRule{
			liveIngressRule("sgr-80", "tcp", "10.0.0.0/8", aws.Int32(80), aws.Int32(80), false, ""),
		},
	}}}
	s := newSGRuleTest(t, fake)

	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{
		{IpProtocol: "tcp", FromPort: intPtr(5000), ToPort: intPtr(5000)},
	}, map[string]string{"ManagedBy": "fabrica"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 0 || fake.tagCalls != 0 {
		t.Errorf("no match: matched=%d tagCalls=%d, want 0/0 (no CreateTags)", n, fake.tagCalls)
	}
}

func TestTagSecurityGroupRulesNoopInputs(t *testing.T) {
	fake := &fakeSGRuleEC2Client{}
	s := newSGRuleTest(t, fake)

	if n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", nil, map[string]string{"ManagedBy": "fabrica"}); n != 0 || err != nil {
		t.Errorf("no specs: got (%d, %v), want (0, nil)", n, err)
	}
	if n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{{IpProtocol: "tcp"}}, nil); n != 0 || err != nil {
		t.Errorf("no tags: got (%d, %v), want (0, nil)", n, err)
	}
	if fake.calls != 0 || fake.tagCalls != 0 {
		t.Errorf("no-op inputs must not call the API: describe=%d tag=%d", fake.calls, fake.tagCalls)
	}
}

func TestTagSecurityGroupRulesAPIErrors(t *testing.T) {
	specs := []sgRuleSpec{{IpProtocol: "tcp"}}
	tags := map[string]string{"ManagedBy": "fabrica"}

	describeFake := &fakeSGRuleEC2Client{describeErr: errors.New("access denied")}
	_, err := newSGRuleTest(t, describeFake).TagSecurityGroupRules(context.Background(), "sg-abc", specs, tags)
	if err == nil {
		t.Fatal("describe error: want error, got nil")
	}
	assertStringContains(t, err.Error(), "describing security group rules")

	createFake := &fakeSGRuleEC2Client{
		createErr: errors.New("tag denied"),
		pages: []ec2.DescribeSecurityGroupRulesOutput{{
			SecurityGroupRules: []types.SecurityGroupRule{
				liveIngressRule("sgr-1", "tcp", "10.0.0.0/8", aws.Int32(5000), aws.Int32(5000), false, ""),
			},
		}},
	}
	_, err = newSGRuleTest(t, createFake).TagSecurityGroupRules(context.Background(), "sg-abc", specs, tags)
	if err == nil {
		t.Fatal("CreateTags error: want error, got nil")
	}
	assertStringContains(t, err.Error(), "tagging security group rules")
}

func TestSgRuleSpecFromDoc(t *testing.T) {
	spec := sgRuleSpecFromDoc(map[string]any{
		"IpProtocol":            "tcp",
		"FromPort":              float64(5000),
		"ToPort":                float64(5000),
		"CidrIp":                "10.0.0.0/8",
		"SourceSecurityGroupId": "sg-agent456",
		"Description":           "ignored field",
	})
	if spec.IpProtocol != "tcp" || spec.CidrIp != "10.0.0.0/8" || spec.SourceSecurityGroupId != "sg-agent456" {
		t.Errorf("spec = %+v", spec)
	}
	if spec.FromPort == nil || *spec.FromPort != 5000 || spec.ToPort == nil || *spec.ToPort != 5000 {
		t.Errorf("ports = %v/%v, want 5000/5000", spec.FromPort, spec.ToPort)
	}

	empty := sgRuleSpecFromDoc(map[string]any{})
	if empty.IpProtocol != "" || empty.FromPort != nil || empty.CidrIp != "" {
		t.Errorf("empty doc spec = %+v, want zero spec", empty)
	}

	// Ports arriving as Go ints (not JSON float64) resolve too.
	intPorts := sgRuleSpecFromDoc(map[string]any{"FromPort": 5000, "ToPort": 5000})
	if intPorts.FromPort == nil || *intPorts.FromPort != 5000 || intPorts.ToPort == nil || *intPorts.ToPort != 5000 {
		t.Errorf("int ports = %v/%v, want 5000/5000", intPorts.FromPort, intPorts.ToPort)
	}
}

// TestTagSecurityGroupRulesFieldMismatch proves each spec field rejects a
// live rule on its own: ToPort, CidrIp, Ipv6CidrIp, and PrefixListId each
// disagree for one rule, and only the fully matching rule is tagged.
func TestTagSecurityGroupRulesFieldMismatch(t *testing.T) {
	spec := sgRuleSpec{
		IpProtocol:   "tcp",
		FromPort:     intPtr(5000),
		ToPort:       intPtr(5001),
		CidrIp:       "10.0.0.0/8",
		Ipv6CidrIp:   "2001:db8::/32",
		PrefixListId: "pl-target",
	}
	rule := func(id, cidr, v6, pl string, to int32) types.SecurityGroupRule {
		r := liveIngressRule(id, "tcp", cidr, aws.Int32(5000), aws.Int32(to), false, "")
		if v6 != "" {
			r.CidrIpv6 = aws.String(v6)
		}
		if pl != "" {
			r.PrefixListId = aws.String(pl)
		}
		return r
	}
	fake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []types.SecurityGroupRule{
			rule("sgr-to", "10.0.0.0/8", "2001:db8::/32", "pl-target", 5000),
			rule("sgr-cidr", "10.1.0.0/8", "2001:db8::/32", "pl-target", 5001),
			rule("sgr-v6", "10.0.0.0/8", "2001:db8:1::/32", "pl-target", 5001),
			rule("sgr-pl", "10.0.0.0/8", "2001:db8::/32", "pl-other", 5001),
			rule("sgr-full", "10.0.0.0/8", "2001:db8::/32", "pl-target", 5001),
		},
	}}}
	s := newSGRuleTest(t, fake)

	n, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{spec}, map[string]string{"ManagedBy": "fabrica"})
	if err != nil {
		t.Fatalf("TagSecurityGroupRules: %v", err)
	}
	if n != 1 || fake.tagCalls != 1 {
		t.Fatalf("matched=%d tagCalls=%d, want 1/1", n, fake.tagCalls)
	}
	if len(fake.lastRuleIDs) != 1 || fake.lastRuleIDs[0] != "sgr-full" {
		t.Errorf("tagged rules = %v, want [sgr-full] (each other rule disagrees on one field)", fake.lastRuleIDs)
	}
}

// TestTagSecurityGroupRulesEnsureClientError covers the lazy-init failure
// before any API call.
func TestTagSecurityGroupRulesEnsureClientError(t *testing.T) {
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(context.Context, string, string) (aws.Config, error) {
			return aws.Config{}, errors.New("no creds")
		},
	}
	_, err := s.TagSecurityGroupRules(context.Background(), "sg-abc", []sgRuleSpec{{IpProtocol: "tcp"}}, map[string]string{"ManagedBy": "fabrica"})
	if err == nil {
		t.Fatal("expected the config-load error")
	}
	assertStringContains(t, err.Error(), "loading AWS config for EC2 service")
}

// TestSgIngressSpecsSkipsMalformedEntries: non-object entries in the
// SecurityGroupIngress array are dropped, well-formed ones kept.
func TestSgIngressSpecsSkipsMalformedEntries(t *testing.T) {
	doc := map[string]any{
		"SecurityGroupIngress": []any{
			"not-an-object",
			nil,
			map[string]any{"IpProtocol": "tcp", "FromPort": float64(5000), "ToPort": float64(5000), "CidrIp": "10.0.0.0/8"},
		},
	}
	specs := sgIngressSpecs(doc)
	if len(specs) != 1 {
		t.Fatalf("specs = %+v, want the single well-formed entry", specs)
	}
	if specs[0].IpProtocol != "tcp" || specs[0].CidrIp != "10.0.0.0/8" || specs[0].FromPort == nil || *specs[0].FromPort != 5000 {
		t.Errorf("spec = %+v", specs[0])
	}
}

func TestStopInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Service{client: fake}

	if err := m.StopInstance(context.Background(), "i-abc123"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if fake.stopCalls != 1 {
		t.Errorf("StopInstances called %d times, want 1", fake.stopCalls)
	}
	if len(fake.lastStopIDs) != 1 || fake.lastStopIDs[0] != "i-abc123" {
		t.Errorf("instance IDs = %v, want [i-abc123]", fake.lastStopIDs)
	}
}

func TestStopInstance_Error(t *testing.T) {
	fake := &fakeEC2Client{stopErr: errors.New("instance not found")}
	m := &ec2Service{client: fake}

	err := m.StopInstance(context.Background(), "i-missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "stopping instance i-missing")
	assertStringContains(t, err.Error(), "instance not found")
}

func TestStartInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Service{client: fake}

	if err := m.StartInstance(context.Background(), "i-xyz789"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if fake.startCalls != 1 {
		t.Errorf("StartInstances called %d times, want 1", fake.startCalls)
	}
	if len(fake.lastStartID) != 1 || fake.lastStartID[0] != "i-xyz789" {
		t.Errorf("instance IDs = %v, want [i-xyz789]", fake.lastStartID)
	}
}

func TestStartInstance_Error(t *testing.T) {
	fake := &fakeEC2Client{startErr: errors.New("throttled")}
	m := &ec2Service{client: fake}

	err := m.StartInstance(context.Background(), "i-xyz789")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "starting instance i-xyz789")
}

func TestEC2ServiceCapabilities_ReturnLoadConfigError(t *testing.T) {
	tests := []struct {
		name string
		run  func(*ec2Service) error
	}{
		{name: "stop", run: func(s *ec2Service) error {
			return s.StopInstance(context.Background(), "i-abc")
		}},
		{name: "start", run: func(s *ec2Service) error {
			return s.StartInstance(context.Background(), "i-abc")
		}},
		{name: "resolve AMI", run: func(s *ec2Service) error {
			_, err := s.ResolveUbuntuAMI(context.Background(), "us-east-1")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &ec2Service{
				awsCfg: awsConfig{region: "us-east-1"},
				loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
					return aws.Config{}, errors.New("no credentials")
				},
			}

			err := tc.run(s)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			assertStringContains(t, err.Error(), "loading AWS config for EC2 service")
		})
	}
}

func TestEC2ServiceEnsureClient_DefaultFactory(t *testing.T) {
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
	}

	if err := s.ensureClient(context.Background()); err != nil {
		t.Fatalf("ensureClient: %v", err)
	}
	if s.client == nil {
		t.Fatal("ensureClient did not create the default EC2 client")
	}
}

func TestEC2ServiceEnsureClient_SharesClientAcrossCapabilities(t *testing.T) {
	fake := &fakeEC2ImagesClient{images: []types.Image{
		{ImageId: aws.String("ami-cached"), CreationDate: aws.String("2025-06-01T00:00:00Z")},
	}}
	loadCalls := 0
	newCalls := 0
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			loadCalls++
			return aws.Config{}, nil
		},
		newClient: func(_ aws.Config) ec2APIClient {
			newCalls++
			return fake
		},
	}

	if _, err := s.ResolveUbuntuAMI(context.Background(), "us-east-1"); err != nil {
		t.Fatalf("ResolveUbuntuAMI: %v", err)
	}
	if err := s.StartInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if err := s.StopInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if loadCalls != 1 {
		t.Errorf("loadCfg called %d times, want 1", loadCalls)
	}
	if newCalls != 1 {
		t.Errorf("newClient called %d times, want 1", newCalls)
	}
}

// TestEC2ServiceEnsureClient_ConcurrentInit verifies concurrent first use
// constructs the SDK client exactly once (the MCP server dispatches tool
// calls concurrently against one shared provider).
func TestEC2ServiceEnsureClient_ConcurrentInit(t *testing.T) {
	var constructions atomic.Int32
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
		newClient: func(aws.Config) ec2APIClient {
			constructions.Add(1)
			return &fakeEC2ImagesClient{}
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.ensureClient(context.Background()); err != nil {
				t.Errorf("ensureClient: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := constructions.Load(); got != 1 {
		t.Errorf("client constructed %d times under concurrency, want 1", got)
	}
}

func intPtr(v int) *int { return &v }
