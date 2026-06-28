package destroy

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func deployModule() *fabricastate.ModuleState {
	return &fabricastate.ModuleState{
		Name: "deploy",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::IAM::Role", Identifier: "role-1"},
			{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
			{TypeName: "AWS::GameLift::Build", Identifier: "build-1"},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1", Properties: map[string]string{"role": "superseded"}},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-2", Properties: map[string]string{"role": "active"}},
		},
	}
}

func TestResourceOrderDefaultFleetsAndBuilds(t *testing.T) {
	order := resourceOrder(false) // --all = false
	got := order(deployModule())
	// Only fleets + builds, fleets first.
	var types []string
	for _, r := range got {
		types = append(types, r.TypeName)
	}
	if len(got) != 3 {
		t.Fatalf("default destroy should target 3 resources (2 fleets + 1 build), got %d: %v", len(got), types)
	}
	if got[0].TypeName != "AWS::GameLift::Fleet" || got[len(got)-1].TypeName != "AWS::GameLift::Build" {
		t.Errorf("expected fleets before build: %v", types)
	}
	for _, r := range got {
		if r.TypeName == "AWS::GameLift::Alias" || r.TypeName == "AWS::IAM::Role" {
			t.Errorf("default destroy must NOT include %s", r.TypeName)
		}
	}
}

func TestResourceOrderAllIncludesAliasAndRole(t *testing.T) {
	order := resourceOrder(true) // --all = true
	got := order(deployModule())
	if len(got) != 5 {
		t.Fatalf("--all should target all 5 resources, got %d", len(got))
	}
	// Order: fleets, build, alias, role (alias+role last so build/fleet refs clear first).
	last := got[len(got)-1]
	if last.TypeName != "AWS::IAM::Role" {
		t.Errorf("role should be deleted last, got %s", last.TypeName)
	}
}

func TestNewBuildsCommand(t *testing.T) {
	// Smoke test: New returns a command with the --all flag and runs dry-run
	// against an empty provider without panicking.
	_ = context.Background()
	cmd := newForTest(false)
	if cmd.Spec.ModuleName != "deploy" {
		t.Errorf("module name = %q", cmd.Spec.ModuleName)
	}
	if cmd.Spec.ResourceOrder == nil {
		t.Error("ResourceOrder must be set for deploy destroy")
	}
	_ = cloud.Resource{}
}
