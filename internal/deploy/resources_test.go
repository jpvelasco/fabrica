package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func setupPlanFixture() *SetupPlan {
	return &SetupPlan{Account: "123456789012", Region: "us-east-1", RoleName: "r", AliasName: "a", BuildBucket: "bkt"}
}
func promotePlanFixture() *PromotePlan {
	return &PromotePlan{
		Account: "123456789012", Region: "us-east-1", BuildVersion: "v1",
		RoleARN: "arn:aws:iam::123456789012:role/r", AliasID: "alias-1",
		FleetName: "fabrica-fleet-v1", BuildName: "fabrica-build-v1",
		InstanceType: "c5.large", FleetType: "ON_DEMAND", LaunchPath: "/local/game/ServerApp",
		BuildOS: "AMAZON_LINUX_2", S3Bucket: "bkt", S3Key: "builds/v1/server.zip",
		FromPort: 7777, ToPort: 7777, DesiredInstances: 2,
	}
}

func TestRoleDesiredState(t *testing.T) {
	raw, err := RoleDesiredState(setupPlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "gamelift.amazonaws.com") {
		t.Error("missing gamelift trust principal")
	}
	if !strings.Contains(s, "arn:aws:s3:::bkt/*") {
		t.Error("missing scoped s3 resource")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestAliasDesiredStateTerminal(t *testing.T) {
	raw, err := AliasDesiredState(setupPlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "TERMINAL") {
		t.Error("setup alias should use TERMINAL routing placeholder")
	}
}

func TestBuildDesiredState(t *testing.T) {
	raw, err := BuildDesiredState(promotePlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"builds/v1/server.zip", "AMAZON_LINUX_2", "\"Version\":\"v1\""} {
		if !strings.Contains(s, want) {
			t.Errorf("build state missing %q in %s", want, s)
		}
	}
}

func TestFleetDesiredState(t *testing.T) {
	raw, err := FleetDesiredState(promotePlanFixture(), "build-123")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"build-123", "c5.large", "EC2", "ON_DEMAND", "/local/game/ServerApp"} {
		if !strings.Contains(s, want) {
			t.Errorf("fleet state missing %q", want)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestFleetDesiredStateCIDRDefault(t *testing.T) {
	p := promotePlanFixture()
	p.AllowedCIDR = defaultAllowedCIDR
	raw, err := FleetDesiredState(p, "build-123")
	if err != nil {
		t.Fatal(err)
	}
	assertInboundCIDR(t, raw, "0.0.0.0/0")
}

func TestFleetDesiredStateCIDROverride(t *testing.T) {
	p := promotePlanFixture()
	p.AllowedCIDR = "192.168.1.0/24"
	raw, err := FleetDesiredState(p, "build-123")
	if err != nil {
		t.Fatal(err)
	}
	assertInboundCIDR(t, raw, "192.168.1.0/24")
}

// assertInboundCIDR verifies the EC2InboundPermissions array carries the
// expected CIDR on the plan's UDP port range.
func assertInboundCIDR(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	perms, ok := doc["EC2InboundPermissions"].([]any)
	if !ok || len(perms) != 1 {
		t.Fatalf("EC2InboundPermissions = %v, want exactly one entry", doc["EC2InboundPermissions"])
	}
	perm := perms[0].(map[string]any)
	if got := perm["IpRange"]; got != want {
		t.Errorf("IpRange = %v, want %s", got, want)
	}
	if got := perm["Protocol"]; got != "UDP" {
		t.Errorf("Protocol = %v, want UDP", got)
	}
}

func TestWarnOpenCIDR(t *testing.T) {
	if got := WarnOpenCIDR("10.0.0.0/8"); got != "" {
		t.Errorf("WarnOpenCIDR(private) = %q, want empty", got)
	}
	got := WarnOpenCIDR("0.0.0.0/0")
	if !strings.Contains(got, "WARNING") || !strings.Contains(got, "0.0.0.0/0") {
		t.Errorf("WarnOpenCIDR(open) = %q, want WARNING mentioning 0.0.0.0/0", got)
	}
}

func TestAliasFlipPatch(t *testing.T) {
	raw, err := AliasFlipPatch("fleet-999")
	if err != nil {
		t.Fatal(err)
	}
	var patch []map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("patch must be a JSON array: %v", err)
	}
	if !strings.Contains(string(raw), "fleet-999") || !strings.Contains(string(raw), "SIMPLE") {
		t.Errorf("patch missing fleet id or SIMPLE routing: %s", raw)
	}
}
