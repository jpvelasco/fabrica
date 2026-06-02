package workstation

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

type fakeVPCResolver struct {
	vpcID    string
	subnetID string
	err      error
}

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return f.vpcID, f.subnetID, f.err
}

func TestNewCreatePlanRequiresAmiID(t *testing.T) {
	cfg := config.WorkstationConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	if !containsStr(err.Error(), "workstation.amiId") {
		t.Errorf("error %q should mention workstation.amiId", err.Error())
	}
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{vpcID: "vpc-default", subnetID: "subnet-default"}

	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != DefaultInstanceType {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, DefaultInstanceType)
	}
	if plan.VolumeSize != DefaultVolumeSize {
		t.Errorf("VolumeSize = %d, want %d", plan.VolumeSize, DefaultVolumeSize)
	}
	if plan.DCVPort != DefaultDCVPort {
		t.Errorf("DCVPort = %d, want %d", plan.DCVPort, DefaultDCVPort)
	}
	if plan.IdleTimeoutMinutes != DefaultIdleTimeoutMinutes {
		t.Errorf("IdleTimeoutMinutes = %d, want %d", plan.IdleTimeoutMinutes, DefaultIdleTimeoutMinutes)
	}
	if plan.VPCID != "vpc-default" {
		t.Errorf("VPCID = %q, want vpc-default", plan.VPCID)
	}
	if plan.DefaultVPC != true {
		t.Error("DefaultVPC should be true when resolver was used")
	}
	if plan.SGName != "fabrica-workstation-sg" {
		t.Errorf("SGName = %q, want fabrica-workstation-sg", plan.SGName)
	}
	if plan.InstanceName != "fabrica-workstation" {
		t.Errorf("InstanceName = %q, want fabrica-workstation", plan.InstanceName)
	}
	if len(plan.CostResources) != 2 {
		t.Errorf("CostResources len = %d, want 2", len(plan.CostResources))
	}
}

func TestNewCreatePlanExplicitVPC(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-explicit",
		SubnetId: "subnet-explicit",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want vpc-explicit", plan.VPCID)
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC IDs are explicit")
	}
}

func TestNewCreatePlanVPCResolverError(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{err: errors.New("no default VPC")}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error when resolver fails")
	}
	if !containsStr(err.Error(), "resolving default VPC") {
		t.Errorf("error %q should mention resolving default VPC", err.Error())
	}
}

func TestNewCreatePlanConfigOverrides(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:              "ami-abc123",
		InstanceType:       "g5.2xlarge",
		VolumeSize:         200,
		IdleTimeoutMinutes: 30,
		AllowedCIDR:        "10.0.0.0/8",
		VPCId:              "vpc-x",
		SubnetId:           "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "g5.2xlarge" {
		t.Errorf("InstanceType = %q, want g5.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 200 {
		t.Errorf("VolumeSize = %d, want 200", plan.VolumeSize)
	}
	if plan.IdleTimeoutMinutes != 30 {
		t.Errorf("IdleTimeoutMinutes = %d, want 30", plan.IdleTimeoutMinutes)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
