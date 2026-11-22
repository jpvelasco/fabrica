package ci

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNamesFollowPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan, err := NewCreatePlan(context.Background(), acme, config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"project", plan.ProjectName, "acme-ci"},
		{"role", plan.RoleName, "acme-ci-codebuild"},
		{"sg", plan.SGName, "acme-ci-sg"},
		{"pipeline", BuildPipelinePlan(acme, config.CIConfig{}).Name, "acme-ci-pipeline"},
		{"cost role", CostResources(acme, config.CIConfig{})[0].Name, "acme-ci-codebuild"},
		{"cost project", CostResources(acme, config.CIConfig{})[1].Name, "acme-ci (BUILD_GENERAL1_SMALL)"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if got := ProjectName(acme, config.CIConfig{ProjectName: "studio"}); got != "studio" {
		t.Errorf("explicit ProjectName = %q, want studio", got)
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan, err := NewCreatePlan(context.Background(), acme, config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	plan.HordeTokenParameter = "/acme/horde/0123456789abcdef0123456789abcdef/service-token"
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var doc struct {
		RoleName string `json:"RoleName"`
		Policies []struct {
			PolicyName string `json:"PolicyName"`
		} `json:"Policies"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal role: %v", err)
	}
	if doc.RoleName != "acme-ci-codebuild" {
		t.Errorf("RoleName = %q, want acme-ci-codebuild", doc.RoleName)
	}
	want := []string{"acme-ci-inline", "acme-ci-horde-token"}
	if len(doc.Policies) != len(want) {
		t.Fatalf("policies = %+v, want %v", doc.Policies, want)
	}
	for i, p := range doc.Policies {
		if p.PolicyName != want[i] {
			t.Errorf("policy[%d] = %q, want %q", i, p.PolicyName, want[i])
		}
	}
}
