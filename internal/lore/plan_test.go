package lore

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanMissingAmiID(t *testing.T) {
	cfg := config.LoreConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	if !strings.Contains(err.Error(), "lore.amiId is required") {
		t.Errorf("error = %q, want lore.amiId is required", err.Error())
	}
	if !strings.Contains(err.Error(), "docs/lore-ami.md") {
		t.Errorf("error = %q, want docs/lore-ami.md", err.Error())
	}
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q, want m5.xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 500 {
		t.Errorf("VolumeSize = %d, want 500", plan.VolumeSize)
	}
	if plan.GRPCPort != DefaultGRPCPort {
		t.Errorf("GRPCPort = %d, want %d", plan.GRPCPort, DefaultGRPCPort)
	}
	if plan.HTTPPort != DefaultHTTPPort {
		t.Errorf("HTTPPort = %d, want %d", plan.HTTPPort, DefaultHTTPPort)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
	if plan.SGName != "fabrica-lore-sg" {
		t.Errorf("SGName = %q, want fabrica-lore-sg", plan.SGName)
	}
	if plan.InstanceName != "fabrica-lore" {
		t.Errorf("InstanceName = %q, want fabrica-lore", plan.InstanceName)
	}
	if plan.AmiID != "ami-abc123" {
		t.Errorf("AmiID = %q, want ami-abc123", plan.AmiID)
	}
}

func TestNewCreatePlanExplicitValues(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		InstanceType: "m5.2xlarge",
		VolumeSize:   1000,
		AllowedCIDR:  "0.0.0.0/0",
		VPCId:        "vpc-explicit",
		SubnetId:     "subnet-explicit",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m5.2xlarge" {
		t.Errorf("InstanceType = %q, want m5.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 1000 {
		t.Errorf("VolumeSize = %d, want 1000", plan.VolumeSize)
	}
	if plan.AllowedCIDR != "0.0.0.0/0" {
		t.Errorf("AllowedCIDR = %q, want 0.0.0.0/0", plan.AllowedCIDR)
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC is explicit")
	}
	if plan.VPCID != "vpc-explicit" || plan.SubnetID != "subnet-explicit" {
		t.Errorf("VPC/subnet = %s/%s", plan.VPCID, plan.SubnetID)
	}
}

func TestNewCreatePlanVPCResolver(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{vpcID: "vpc-fake", subnetID: "subnet-fake"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.DefaultVPC {
		t.Error("DefaultVPC should be true when resolver fills VPC")
	}
	if plan.VPCID != "vpc-fake" || plan.SubnetID != "subnet-fake" {
		t.Errorf("VPC/subnet = %s/%s", plan.VPCID, plan.SubnetID)
	}
}

func TestNewCreatePlanVPCResolverError(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{err: errFakeVPC}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error from resolver")
	}
	if !strings.Contains(err.Error(), "resolving default VPC") {
		t.Errorf("error = %q", err.Error())
	}
}

type fakeVPCResolver struct {
	vpcID, subnetID string
	err             error
}

func (f *fakeVPCResolver) ResolveDefaultVPC(context.Context) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	return f.vpcID, f.subnetID, nil
}

var errFakeVPC = errString("no default VPC")

type errString string

func (e errString) Error() string { return string(e) }
