package horde

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanMissingAmiID(t *testing.T) {
	cfg := config.HordeConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assertContains(t, err.Error(), "horde.amiId is required")
	assertContains(t, err.Error(), "docs/horde-ami.md")
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.HordeConfig{AmiID: "ami-abc123"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m7i.2xlarge" {
		t.Errorf("InstanceType = %q, want m7i.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 100 {
		t.Errorf("VolumeSize = %d, want 100", plan.VolumeSize)
	}
	if plan.Port != 5000 {
		t.Errorf("Port = %d, want 5000", plan.Port)
	}
	if plan.GRPCPort != 5002 {
		t.Errorf("GRPCPort = %d, want 5002", plan.GRPCPort)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
	if plan.SGName != "fabrica-horde-sg" {
		t.Errorf("SGName = %q, want fabrica-horde-sg", plan.SGName)
	}
	if plan.InstanceName != "fabrica-horde" {
		t.Errorf("InstanceName = %q, want fabrica-horde", plan.InstanceName)
	}
	if plan.Account != "123456789012" {
		t.Errorf("Account = %q, want 123456789012", plan.Account)
	}
	if plan.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", plan.Region)
	}
	if plan.AmiID != "ami-abc123" {
		t.Errorf("AmiID = %q, want ami-abc123", plan.AmiID)
	}
}

func TestNewCreatePlanExplicitValues(t *testing.T) {
	cfg := config.HordeConfig{
		AmiID:        "ami-abc123",
		InstanceType: "m7i.2xlarge",
		VolumeSize:   200,
		Port:         5001,
		GRPCPort:     5003,
		AllowedCIDR:  "0.0.0.0/0",
		VPCId:        "vpc-explicit",
		SubnetId:     "subnet-explicit",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m7i.2xlarge" {
		t.Errorf("InstanceType = %q, want m7i.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 200 {
		t.Errorf("VolumeSize = %d, want 200", plan.VolumeSize)
	}
	if plan.Port != 5001 {
		t.Errorf("Port = %d, want 5001", plan.Port)
	}
	if plan.GRPCPort != 5003 {
		t.Errorf("GRPCPort = %d, want 5003", plan.GRPCPort)
	}
	if plan.AllowedCIDR != "0.0.0.0/0" {
		t.Errorf("AllowedCIDR = %q, want 0.0.0.0/0", plan.AllowedCIDR)
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC is explicit")
	}
}

func TestNewCreatePlanVPCResolver(t *testing.T) {
	cfg := config.HordeConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{vpcID: "vpc-fake", subnetID: "subnet-fake"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-fake" {
		t.Errorf("VPCID = %q, want vpc-fake", plan.VPCID)
	}
	if plan.SubnetID != "subnet-fake" {
		t.Errorf("SubnetID = %q, want subnet-fake", plan.SubnetID)
	}
	if !plan.DefaultVPC {
		t.Error("DefaultVPC should be true when resolver was used")
	}
}

func TestNewCreatePlanVPCResolverError(t *testing.T) {
	cfg := config.HordeConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{err: errors.New("no default VPC")}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error when resolver fails")
	}
	assertContains(t, err.Error(), "resolving default VPC")
}

func TestNewCreatePlanExplicitVPCSkipsResolver(t *testing.T) {
	cfg := config.HordeConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-explicit",
		SubnetId: "subnet-explicit",
	}
	called := false
	resolver := &fakeVPCResolver{vpcID: "vpc-should-not-be-used", callTracker: &called}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("resolver should not be called when VPC is explicitly configured")
	}
	if plan.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want vpc-explicit", plan.VPCID)
	}
}

func TestNewCreatePlanCostResources(t *testing.T) {
	cfg := config.HordeConfig{AmiID: "ami-abc123"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.CostResources) != 2 {
		t.Fatalf("CostResources len = %d, want 2", len(plan.CostResources))
	}
	if plan.CostResources[0].TypeName != TypeAWSEC2Instance {
		t.Errorf("CostResources[0].TypeName = %q, want %q", plan.CostResources[0].TypeName, TypeAWSEC2Instance)
	}
	if plan.CostResources[0].Name != "m7i.2xlarge" {
		t.Errorf("CostResources[0].Name = %q, want m7i.2xlarge", plan.CostResources[0].Name)
	}
	if plan.CostResources[1].TypeName != TypeAWSEC2Volume {
		t.Errorf("CostResources[1].TypeName = %q, want %q", plan.CostResources[1].TypeName, TypeAWSEC2Volume)
	}
	if plan.CostResources[1].Name != "gp3-100GiB" {
		t.Errorf("CostResources[1].Name = %q, want gp3-100GiB", plan.CostResources[1].Name)
	}
}

// fakeVPCResolver is a test double for VPCResolver.
type fakeVPCResolver struct {
	vpcID       string
	subnetID    string
	err         error
	callTracker *bool
}

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	if f.callTracker != nil {
		*f.callTracker = true
	}
	return f.vpcID, f.subnetID, f.err
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, sub)
}
