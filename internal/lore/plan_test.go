package lore

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanMissingAmiID(t *testing.T) {
	cfg := config.LoreConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assert.Contains(t, err.Error(), "lore.amiId is required")
	assert.Contains(t, err.Error(), "docs/lore-ami.md")
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
	// Default store backend is local.
	if plan.StoreBackend != StoreBackendLocal {
		t.Errorf("StoreBackend = %q, want local", plan.StoreBackend)
	}
	if plan.StoreBucket != "" {
		t.Errorf("StoreBucket = %q, want empty for local backend", plan.StoreBucket)
	}
}

func TestNewCreatePlanS3StoreBackend(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "s3",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.StoreBackend != StoreBackendS3 {
		t.Errorf("StoreBackend = %q, want s3", plan.StoreBackend)
	}
	if plan.StoreBucket != "fabrica-lore-store-123456789012-us-east-1" {
		t.Errorf("StoreBucket = %q, want fabrica-lore-store-123456789012-us-east-1", plan.StoreBucket)
	}
	if plan.RoleName != "fabrica-lore-role" {
		t.Errorf("RoleName = %q, want fabrica-lore-role", plan.RoleName)
	}
	if plan.InstanceProfileName != "fabrica-lore-profile" {
		t.Errorf("InstanceProfileName = %q, want fabrica-lore-profile", plan.InstanceProfileName)
	}
}

func TestNewCreatePlanS3StoreCustomBucket(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "s3",
		StoreBucket:  "my-lore-bucket",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.StoreBucket != "my-lore-bucket" {
		t.Errorf("StoreBucket = %q, want my-lore-bucket", plan.StoreBucket)
	}
}

func TestNewCreatePlanInvalidStoreBackend(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "invalid",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid backend should fall back to local.
	if plan.StoreBackend != StoreBackendLocal {
		t.Errorf("StoreBackend = %q, want local for invalid input", plan.StoreBackend)
	}
}

func TestNewCreatePlanTLSConfig(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID: "ami-abc123",
		TLSConfig: config.LoreTLSConfig{
			Enabled:  true,
			CertPath: "/etc/ssl/certs/lore.crt",
			KeyPath:  "/etc/ssl/private/lore.key",
		},
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.TLSConfig.Enabled {
		t.Error("TLSConfig.Enabled should be true")
	}
	if plan.TLSConfig.CertPath != "/etc/ssl/certs/lore.crt" {
		t.Errorf("TLSConfig.CertPath = %q", plan.TLSConfig.CertPath)
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
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-fake", SubnetID: "subnet-fake"}
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
	resolver := &cloud.TestVPCResolver{Err: errFakeVPC}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error from resolver")
	}
	assert.Contains(t, err.Error(), "resolving default VPC")
}

var errFakeVPC = errString("no default VPC")

type errString string

func (e errString) Error() string { return string(e) }
