package deploy

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewSetupPlanDefaults(t *testing.T) {
	p := NewSetupPlan(config.DeployConfig{}, "123456789012", "us-east-1")
	if p.RoleName != defaultRoleName {
		t.Errorf("RoleName = %q, want %q", p.RoleName, defaultRoleName)
	}
	if p.AliasName != defaultAliasName {
		t.Errorf("AliasName = %q, want %q", p.AliasName, defaultAliasName)
	}
}

func TestNewSetupPlanOverrides(t *testing.T) {
	p := NewSetupPlan(config.DeployConfig{RoleName: "my-role", AliasName: "my-alias"}, "123456789012", "us-east-1")
	if p.RoleName != "my-role" || p.AliasName != "my-alias" {
		t.Errorf("overrides not applied: %+v", p)
	}
}

func TestNewPromotePlanDefaultsAndS3(t *testing.T) {
	p := NewPromotePlan(config.DeployConfig{BuildBucket: "bkt"}, "123456789012", "us-east-1",
		"v1.2.3", "arn:aws:iam::123456789012:role/fabrica-deploy", "alias-1", "", "")
	if p.InstanceType != defaultInstanceType {
		t.Errorf("InstanceType = %q, want %q", p.InstanceType, defaultInstanceType)
	}
	if p.FleetType != defaultFleetType {
		t.Errorf("FleetType = %q, want %q", p.FleetType, defaultFleetType)
	}
	if p.DesiredInstances != defaultDesiredInstances {
		t.Errorf("DesiredInstances = %d", p.DesiredInstances)
	}
	if p.ActivationTimeoutMinutes != defaultActivationTimeoutMinutes {
		t.Errorf("ActivationTimeoutMinutes = %d", p.ActivationTimeoutMinutes)
	}
	// S3 defaults: bucket from config, key from build-version convention.
	if p.S3Bucket != "bkt" {
		t.Errorf("S3Bucket = %q", p.S3Bucket)
	}
	if p.S3Key != "builds/v1.2.3/server.zip" {
		t.Errorf("S3Key = %q", p.S3Key)
	}
	// Fleet/build names incorporate the sanitized build version.
	if p.FleetName == "" || p.BuildName == "" {
		t.Errorf("names empty: %+v", p)
	}
	// Cost resource encodes instance type + count.
	if len(p.CostResources) == 0 || p.CostResources[0].TypeName != TypeGameLiftFleet {
		t.Errorf("CostResources = %+v", p.CostResources)
	}
}

func TestNewPromotePlanExplicitS3(t *testing.T) {
	p := NewPromotePlan(config.DeployConfig{}, "123456789012", "us-east-1",
		"v1", "arn:role", "alias-1", "other-bucket", "custom/key.zip")
	if p.S3Bucket != "other-bucket" || p.S3Key != "custom/key.zip" {
		t.Errorf("explicit S3 not honored: %+v", p)
	}
}

func TestFleetCostName(t *testing.T) {
	if got := fleetCostName("c5.large", 2); got != "c5.largex2" {
		t.Errorf("fleetCostName = %q", got)
	}
}
