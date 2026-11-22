package deploy

import (
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNamesFollowPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	setup := NewSetupPlan(acme, config.DeployConfig{}, "123456789012", "us-east-1")
	if setup.RoleName != "acme-deploy-gamelift" || setup.AliasName != "acme-deploy" {
		t.Errorf("setup role/alias = %q/%q, want acme-deploy-gamelift/acme-deploy", setup.RoleName, setup.AliasName)
	}
	promote, err := NewPromotePlan(acme, config.DeployConfig{BuildBucket: "bkt"}, "123456789012", "us-east-1", "v1.2", "arn:role", "alias-1", "", "")
	if err != nil {
		t.Fatalf("NewPromotePlan: %v", err)
	}
	if promote.FleetName != "acme-fleet-v1-2" || promote.BuildName != "acme-build-v1-2" {
		t.Errorf("fleet/build = %q/%q, want acme-fleet-v1-2/acme-build-v1-2", promote.FleetName, promote.BuildName)
	}
	if promote.RoleName != "acme-deploy-gamelift" {
		t.Errorf("promote RoleName = %q, want acme-deploy-gamelift", promote.RoleName)
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan := NewSetupPlan(acme, config.DeployConfig{BuildBucket: "bkt"}, "123456789012", "us-east-1")
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
	if doc.RoleName != "acme-deploy-gamelift" || len(doc.Policies) != 1 || doc.Policies[0].PolicyName != "acme-deploy-s3-read" {
		t.Errorf("role = %+v, want acme-deploy-gamelift with acme-deploy-s3-read", doc)
	}
}

func TestConfiguredRoleOverridesPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan := NewSetupPlan(acme, config.DeployConfig{RoleName: "my-role"}, "123456789012", "us-east-1")
	if plan.RoleName != "my-role" {
		t.Errorf("RoleName = %q, want configured my-role", plan.RoleName)
	}
}
