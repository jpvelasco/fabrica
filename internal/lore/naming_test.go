package lore

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

var acme = naming.MustResolve("acme")

func TestNewCreatePlanNamesFollowPrefix(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-1", StoreBackend: "s3"}
	plan, err := NewCreatePlan(context.Background(), acme, cfg, "123456789012", "us-east-1", nil, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	got := []string{plan.SGName, plan.InstanceName, plan.RoleName, plan.InstanceProfileName, plan.StoreBucket}
	want := []string{"acme-lore-sg", "acme-lore", "acme-lore-role", "acme-lore-profile", "acme-lore-store-123456789012-us-east-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, table := range plan.StoreTables {
		if !strings.HasPrefix(table, "acme-lore-store-123456789012-us-east-1-") {
			t.Errorf("store table %q does not derive from the prefixed bucket", table)
		}
	}
}

func TestRoleAndInstanceFollowPrefix(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), acme, config.LoreConfig{AmiID: "ami-1", StoreBackend: "s3"}, "123456789012", "us-east-1", nil, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	role, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	for _, want := range []string{"acme-lore-store-s3", "acme-lore-store-dynamodb", "acme-ssm-output", "log-group:/acme/ssm/*"} {
		if !strings.Contains(string(role), want) {
			t.Errorf("role document missing %q", want)
		}
	}
	if strings.Contains(string(role), "fabrica-") {
		t.Errorf("role document still carries a fabrica name: %s", role)
	}
	plan.RootDeviceName = "/dev/sda1"
	inst, err := InstanceDesiredState(plan, "sg-1", "#cloud-config")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if !strings.Contains(string(inst), `"Value":"acme-lore"`) {
		t.Errorf("instance Name tag does not carry the plan name: %s", inst)
	}
}

func TestCostResourcesFollowPrefix(t *testing.T) {
	for _, r := range CostResources(acme, config.LoreConfig{StoreBackend: "s3"}) {
		if r.TypeName == cloud.TypeAWSS3Bucket {
			if r.Name != "acme-lore-store" {
				t.Errorf("cost bucket = %q, want acme-lore-store", r.Name)
			}
			return
		}
	}
	t.Fatal("no S3 bucket cost resource for the s3 backend")
}
