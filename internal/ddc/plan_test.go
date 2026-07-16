package ddc

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewSetupPlanZenDefaults(t *testing.T) {
	plan, err := NewSetupPlan(context.Background(), config.DDCConfig{
		AmiID:    "ami-ddc",
		VPCId:    "vpc-1",
		SubnetId: "subnet-1",
	}, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("NewSetupPlan: %v", err)
	}
	if plan.Backend != BackendZen {
		t.Fatalf("Backend = %q", plan.Backend)
	}
	if plan.InstanceType != DefaultInstanceType || plan.VolumeSize != DefaultVolumeSize {
		t.Fatalf("instance defaults: %s %d", plan.InstanceType, plan.VolumeSize)
	}
	if plan.Bucket != "fabrica-ddc-123456789012-us-east-1" {
		t.Fatalf("Bucket = %q", plan.Bucket)
	}
	if plan.PublicPort != 80 || plan.AllowedCIDR != DefaultAllowedCIDR {
		t.Fatalf("ports/cidr: %d %s", plan.PublicPort, plan.AllowedCIDR)
	}
	if err := plan.Topology.Validate(); err != nil {
		t.Fatalf("topology: %v", err)
	}
	if plan.Topology.HomeRegion != "us-east-1" {
		t.Fatalf("HomeRegion = %q", plan.Topology.HomeRegion)
	}
}

func TestNewSetupPlanRequiresAmi(t *testing.T) {
	_, err := NewSetupPlan(context.Background(), config.DDCConfig{}, "1", "us-east-1", nil)
	if err == nil || !strings.Contains(err.Error(), "amiId") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewSetupPlanScyllaRequiresAmi(t *testing.T) {
	_, err := NewSetupPlan(context.Background(), config.DDCConfig{
		AmiID:    "ami-ddc",
		Backend:  BackendScylla,
		VPCId:    "vpc-1",
		SubnetId: "subnet-1",
	}, "1", "us-east-1", nil)
	if err == nil || !strings.Contains(err.Error(), "scyllaAmiId") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewSetupPlanScyllaOK(t *testing.T) {
	plan, err := NewSetupPlan(context.Background(), config.DDCConfig{
		AmiID:       "ami-ddc",
		ScyllaAmiID: "ami-scylla",
		Backend:     BackendScylla,
		VPCId:       "vpc-1",
		SubnetId:    "subnet-1",
	}, "1", "us-east-1", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if plan.Backend != BackendScylla {
		t.Fatalf("backend = %q", plan.Backend)
	}
}

func TestNewSetupPlanBadBackend(t *testing.T) {
	_, err := NewSetupPlan(context.Background(), config.DDCConfig{
		AmiID: "ami-x", Backend: "mongo", VPCId: "v", SubnetId: "s",
	}, "1", "r", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWarnOpenCIDR(t *testing.T) {
	if WarnOpenCIDR("10.0.0.0/8") != "" {
		t.Fatal("expected empty")
	}
	w := WarnOpenCIDR("0.0.0.0/0")
	if !strings.Contains(w, "WARNING") || !strings.Contains(w, "0.0.0.0/0") {
		t.Fatalf("warn = %q", w)
	}
}

func TestWarnScyllaBootstrap(t *testing.T) {
	w := WarnScyllaBootstrap()
	if !strings.Contains(w, "single-node") || !strings.Contains(w, "not production HA") {
		t.Fatalf("warn = %q", w)
	}
}
