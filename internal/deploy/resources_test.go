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
