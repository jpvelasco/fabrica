package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

// postCreateCreateFakes wires a successful Cloud Control create whose waiter
// returns the given identifier.
func postCreateCreateFakes(identifier string) (*fakeCCClient, *fakeCCWaiter) {
	cc := &fakeCCClient{createOut: &cloudcontrol.CreateResourceOutput{
		ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
	}}
	waiter := &fakeCCWaiter{out: &cloudcontrol.GetResourceRequestStatusOutput{
		ProgressEvent: &types.ProgressEvent{
			OperationStatus: types.OperationStatusSuccess,
			Identifier:      awssdk.String(identifier),
		},
	}}
	return cc, waiter
}

// TestCreateInstanceProfileTagsPostCreate verifies the post-create hook tags
// IAM instance profiles (whose create schema has no Tags property) with the
// protected standard set plus additive operator tags — operator entries that
// shadow identity tags are rejected.
func TestCreateInstanceProfileTagsPostCreate(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-perforce-profile")
	rc := newCCTestClients(cc, waiter)
	iamFake := &fakeIAMClient{}
	rc.iam = &iamService{client: iamFake}
	rc.operatorTags = map[string]string{"env": "staging", "ManagedBy": "operator-override"}

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-perforce-profile","Roles":["fabrica-perforce-role"]}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iamFake.calls != 1 {
		t.Fatalf("TagInstanceProfile calls = %d, want 1", iamFake.calls)
	}
	if awssdk.ToString(iamFake.lastInput.InstanceProfileName) != "fabrica-perforce-profile" {
		t.Errorf("profile name = %q", awssdk.ToString(iamFake.lastInput.InstanceProfileName))
	}
	got := map[string]string{}
	for _, tg := range iamFake.lastInput.Tags {
		got[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "fabrica", "FabricaVersion": "test", "env": "staging"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q (tags: %v)", k, got[k], v, got)
		}
	}
}

// TestCreateInstanceProfileTagErrorFailsCreate verifies an IAM tagging error
// fails the create so the operator sees the resource exists but is
// untagged — the re-run re-enters the hook via AlreadyExists recovery.
func TestCreateInstanceProfileTagErrorFailsCreate(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-horde-profile")
	rc := newCCTestClients(cc, waiter)
	rc.iam = &iamService{client: &fakeIAMClient{tagErr: errors.New("access denied")}}

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-horde-profile","Roles":["fabrica-horde-role"]}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("want tagging error, got nil")
	}
	assertStringContains(t, err.Error(), "tagging IAM instance profile fabrica-horde-profile")
}

func TestCreateSecurityGroupTagsInlineIngressRules(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sg-abc123")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []ec2types.SecurityGroupRule{
			liveIngressRule("sgr-match", "tcp", "10.0.0.0/8", awssdk.Int32(5000), awssdk.Int32(5000), false, ""),
			liveIngressRule("sgr-miss", "tcp", "10.0.0.0/8", awssdk.Int32(80), awssdk.Int32(80), false, ""),
			liveIngressRule("sgr-egress", "tcp", "10.0.0.0/8", awssdk.Int32(5000), awssdk.Int32(5000), true, ""),
		},
	}}}
	rc.instances = &ec2Service{client: sgFake}
	rc.operatorTags = map[string]string{"env": "staging"}

	r := &fabricac.Resource{
		TypeName: "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{
			"GroupName": "fabrica-horde-sg",
			"VpcId": "vpc-123",
			"SecurityGroupIngress": [
				{"IpProtocol": "tcp", "FromPort": 5000, "ToPort": 5000, "CidrIp": "10.0.0.0/8"}
			]
		}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sgFake.tagCalls != 1 {
		t.Fatalf("CreateTags calls = %d, want 1", sgFake.tagCalls)
	}
	if len(sgFake.lastRuleIDs) != 1 || sgFake.lastRuleIDs[0] != "sgr-match" {
		t.Errorf("tagged rules = %v, want [sgr-match] (port-mismatched and egress rules excluded)", sgFake.lastRuleIDs)
	}
	got := map[string]string{}
	for _, tg := range sgFake.lastTags {
		got[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	// Tag set is the final (post-injectFabricaTags) desired-state Tags array.
	if got["ManagedBy"] != "fabrica" || got["FabricaVersion"] != "test" || got["env"] != "staging" {
		t.Errorf("tags = %v, want ManagedBy=fabrica FabricaVersion=test env=staging", got)
	}
}

func TestCreateSecurityGroupNoIngressSkipsRuleTagging(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sg-noops")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{}
	rc.instances = &ec2Service{client: sgFake}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"fabrica-lore-sg","VpcId":"vpc-123"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sgFake.calls != 0 || sgFake.tagCalls != 0 {
		t.Errorf("no ingress rules: describe=%d tag=%d, want 0/0", sgFake.calls, sgFake.tagCalls)
	}
}

func TestCreateSecurityGroupRuleDescribeErrorFailsCreate(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sg-abc123")
	rc := newCCTestClients(cc, waiter)
	rc.instances = &ec2Service{client: &fakeSGRuleEC2Client{describeErr: errors.New("access denied")}}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"sg","VpcId":"vpc-1","SecurityGroupIngress":[{"IpProtocol":"tcp","FromPort":5000,"ToPort":5000,"CidrIp":"10.0.0.0/8"}]}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("want describe error, got nil")
	}
	assertStringContains(t, err.Error(), "describing security group rules")
}

// TestCreateSecurityGroupNoRuleMatchWarns verifies a rule set that no longer
// matches the live group (operator edited it out of band) is a warning, not a
// failure: the group exists, and the re-run re-tags on the next create.
func TestCreateSecurityGroupNoRuleMatchWarns(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	cc, waiter := postCreateCreateFakes("sg-abc123")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []ec2types.SecurityGroupRule{
			liveIngressRule("sgr-80", "tcp", "10.0.0.0/8", awssdk.Int32(80), awssdk.Int32(80), false, ""),
		},
	}}}
	rc.instances = &ec2Service{client: sgFake}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"sg","VpcId":"vpc-1","SecurityGroupIngress":[{"IpProtocol":"tcp","FromPort":5000,"ToPort":5000,"CidrIp":"10.0.0.0/8"}]}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v (no match must not fail the create)", err)
	}
	if sgFake.tagCalls != 0 {
		t.Errorf("CreateTags calls = %d, want 0", sgFake.tagCalls)
	}
	if !strings.Contains(buf.String(), "no live security group rule matched") {
		t.Errorf("log missing no-match warning: %s", buf.String())
	}
}

func TestCreateStandaloneIngressTagsRule(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sgr-standalone-1")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{pages: []ec2.DescribeSecurityGroupRulesOutput{{
		SecurityGroupRules: []ec2types.SecurityGroupRule{
			liveIngressRule("sgr-agent-rule", "tcp", "", awssdk.Int32(5000), awssdk.Int32(5000), false, "sg-agent456"),
			liveIngressRule("sgr-other-rule", "tcp", "", awssdk.Int32(5000), awssdk.Int32(5000), false, "sg-other789"),
		},
	}}}
	rc.instances = &ec2Service{client: sgFake}
	rc.operatorTags = map[string]string{"team": "platform"}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroupIngress",
		DesiredState: json.RawMessage(`{"GroupId":"sg-coordinator","SourceSecurityGroupId":"sg-agent456","IpProtocol":"tcp","FromPort":5000,"ToPort":5000}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sgFake.tagCalls != 1 {
		t.Fatalf("CreateTags calls = %d, want 1", sgFake.tagCalls)
	}
	if len(sgFake.lastRuleIDs) != 1 || sgFake.lastRuleIDs[0] != "sgr-agent-rule" {
		t.Errorf("tagged rules = %v, want [sgr-agent-rule] (matched via referenced source SG)", sgFake.lastRuleIDs)
	}
	// The standalone ingress doc carries no Tags property: the protected
	// standard set plus operator extras applies.
	got := map[string]string{}
	for _, tg := range sgFake.lastTags {
		got[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	if got["ManagedBy"] != "fabrica" || got["FabricaVersion"] != "test" || got["team"] != "platform" {
		t.Errorf("tags = %v, want ManagedBy=fabrica FabricaVersion=test team=platform", got)
	}
}

func TestCreateStandaloneIngressMissingGroupIdSkipsTagging(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(slog.LevelInfo, true, &buf)

	cc, waiter := postCreateCreateFakes("sgr-noid-1")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{}
	rc.instances = &ec2Service{client: sgFake}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroupIngress",
		DesiredState: json.RawMessage(`{"SourceSecurityGroupId":"sg-agent456","IpProtocol":"tcp","FromPort":5000,"ToPort":5000}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v (missing GroupId must not fail the create)", err)
	}
	if sgFake.calls != 0 || sgFake.tagCalls != 0 {
		t.Errorf("describe=%d tag=%d, want 0/0", sgFake.calls, sgFake.tagCalls)
	}
	if !strings.Contains(buf.String(), "carries no GroupId") {
		t.Errorf("log missing GroupId warning: %s", buf.String())
	}
}

// TestPostCreateTags covers the tag-set resolution: the final desired-state
// "Tags" array wins when present; otherwise the protected standard set with
// operator extras applies.
func TestPostCreateTags(t *testing.T) {
	// With Tags in the doc: the exact set applied to the resource.
	withTags := json.RawMessage(`{"Tags":[{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaModule","Value":"ddc"},{"Key":"env","Value":"prod"}]}`)
	got := postCreateTags(withTags, "0.5.0", map[string]string{"team": "platform"})
	if got["FabricaModule"] != "ddc" || got["env"] != "prod" {
		t.Errorf("doc Tags must win: %v", got)
	}

	// Without Tags: the protected standard set + additive operator extras.
	fallback := postCreateTags(json.RawMessage(`{"InstanceProfileName":"p"}`), "0.5.0", map[string]string{"team": "platform", "ManagedBy": "operator"})
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "fabrica", "FabricaVersion": "0.5.0", "team": "platform"}
	if len(fallback) != len(want) {
		t.Errorf("fallback = %v, want %v", fallback, want)
	}
	for k, v := range want {
		if fallback[k] != v {
			t.Errorf("fallback tag %s = %q, want %q (tags: %v)", k, fallback[k], v, fallback)
		}
	}

	// Non-JSON: the protected standard set.
	if got := postCreateTags(json.RawMessage(`not json`), "0.5.0", nil); got["ManagedBy"] != "fabrica" || got["FabricaVersion"] != "0.5.0" {
		t.Errorf("non-JSON fallback = %v", got)
	}
}

// TestCreateInstanceProfileSkipsHookWithoutIAMClient: providers without the
// IAM auxiliary seam (fakes, E2E) still complete instance-profile creates —
// the post-create hook is a no-op there.
func TestCreateInstanceProfileSkipsHookWithoutIAMClient(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-perforce-profile")
	rc := newCCTestClients(cc, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-perforce-profile"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Identifier != "fabrica-perforce-profile" {
		t.Errorf("identifier = %q, want the waiter-provided value", r.Identifier)
	}
}

// TestCreateSecurityGroupSkipsHookWithoutEC2Client: the SG path completes
// with the EC2 auxiliary seam absent.
func TestCreateSecurityGroupSkipsHookWithoutEC2Client(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sg-123")
	rc := newCCTestClients(cc, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"fabrica-workstation-sg","SecurityGroupIngress":[{"IpProtocol":"tcp","FromPort":8443,"ToPort":8443,"CidrIp":"10.0.0.0/8"}]}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// TestCreateSecurityGroupUnparseableDocSkipsHook: with a live EC2 client, a
// desired state that does not parse as JSON skips rule tagging (the hook
// cannot extract specs from it) and the create still completes.
func TestCreateSecurityGroupUnparseableDocSkipsHook(t *testing.T) {
	cc, waiter := postCreateCreateFakes("sg-456")
	rc := newCCTestClients(cc, waiter)
	sgFake := &fakeSGRuleEC2Client{}
	rc.instances = &ec2Service{client: sgFake}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`not-json`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Identifier != "sg-456" {
		t.Errorf("identifier = %q, want sg-456", r.Identifier)
	}
	if sgFake.tagCalls != 0 {
		t.Errorf("tag calls = %d, want 0", sgFake.tagCalls)
	}
}
