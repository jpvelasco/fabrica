package workstation

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanRequiresAmiID(t *testing.T) {
	cfg := config.WorkstationConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "", "")
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assert.Contains(t, err.Error(), "workstation.amiId")
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-default", SubnetID: "subnet-default"}

	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver, "", "")
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
	if !plan.DefaultVPC {
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
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "", "")
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
	resolver := &cloud.TestVPCResolver{Err: errors.New("no default VPC")}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver, "", "")
	if err == nil {
		t.Fatal("expected error when resolver fails")
	}
	assert.Contains(t, err.Error(), "resolving default VPC")
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
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "", "")
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

func TestNewCreatePlanTemplateArtist(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-x",
		SubnetId: "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, TemplateArtist, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != ArtistInstanceType {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, ArtistInstanceType)
	}
	if plan.VolumeSize != ArtistVolumeSize {
		t.Errorf("VolumeSize = %d, want %d", plan.VolumeSize, ArtistVolumeSize)
	}
}

// TestResolveSizingFlagOverridesTemplate pins the documented precedence:
// explicit flags/config win over template values per field.
func TestResolveSizingFlagOverridesTemplate(t *testing.T) {
	cfg := config.WorkstationConfig{InstanceType: "c7i.xlarge", VolumeSize: 50}
	inst, vol := resolveSizing(cfg, TemplateArtist)
	if inst != "c7i.xlarge" || vol != 50 {
		t.Errorf("resolveSizing(flag+artist) = (%q, %d), want (c7i.xlarge, 50)", inst, vol)
	}
}

// TestResolveSizingTemplateFillsUnsetField verifies per-field fallback: a
// template still supplies the field the operator did not set.
func TestResolveSizingTemplateFillsUnsetField(t *testing.T) {
	cfg := config.WorkstationConfig{VolumeSize: 50}
	inst, vol := resolveSizing(cfg, TemplateArtist)
	if inst != ArtistInstanceType || vol != 50 {
		t.Errorf("resolveSizing(vol-only + artist) = (%q, %d), want (%q, 50)", inst, vol, ArtistInstanceType)
	}

	cfg2 := config.WorkstationConfig{InstanceType: "c7i.xlarge"}
	inst2, vol2 := resolveSizing(cfg2, TemplateProgrammer)
	if inst2 != "c7i.xlarge" || vol2 != ProgrammerVolumeSize {
		t.Errorf("resolveSizing(type-only + programmer) = (%q, %d), want (c7i.xlarge, %d)", inst2, vol2, ProgrammerVolumeSize)
	}
}

// TestNewCreatePlanTemplateCostMatchesSizing verifies the pre-approval cost
// estimate prices the template-resolved shape, not the raw config defaults.
func TestNewCreatePlanTemplateCostMatchesSizing(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-x",
		SubnetId: "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, TemplateArtist, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.CostResources) != 2 {
		t.Fatalf("CostResources len = %d, want 2", len(plan.CostResources))
	}
	if plan.CostResources[0].Name != ArtistInstanceType {
		t.Errorf("cost instance = %q, want %q", plan.CostResources[0].Name, ArtistInstanceType)
	}
	wantVol := "gp3-" + strconv.Itoa(ArtistVolumeSize) + "GiB"
	if plan.CostResources[1].Name != wantVol {
		t.Errorf("cost volume = %q, want %q", plan.CostResources[1].Name, wantVol)
	}
	if plan.InstanceType != plan.CostResources[0].Name {
		t.Error("plan shape and cost shape disagree")
	}
}

func TestNewCreatePlanTemplateProgrammer(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-x",
		SubnetId: "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, TemplateProgrammer, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != ProgrammerInstanceType {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, ProgrammerInstanceType)
	}
	if plan.VolumeSize != ProgrammerVolumeSize {
		t.Errorf("VolumeSize = %d, want %d", plan.VolumeSize, ProgrammerVolumeSize)
	}
}

func TestNewCreatePlanTemplateUnknownErrors(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "designer", "")
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
	assert.Contains(t, err.Error(), "unknown template")
}

func TestNewCreatePlanMountPerforce(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-x",
		SubnetId: "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "", "10.0.1.5:1666")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.MountPerforce {
		t.Error("MountPerforce should be true when perforceAddr is non-empty")
	}
	if plan.PerforceServerAddr != "10.0.1.5:1666" {
		t.Errorf("PerforceServerAddr = %q, want 10.0.1.5:1666", plan.PerforceServerAddr)
	}
}

func TestNewCreatePlanSSMRoleAndProfile(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-x",
		SubnetId: "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.RoleName != "fabrica-workstation-role" {
		t.Errorf("RoleName = %q, want fabrica-workstation-role", plan.RoleName)
	}
	if plan.InstanceProfileName != "fabrica-workstation-profile" {
		t.Errorf("InstanceProfileName = %q, want fabrica-workstation-profile", plan.InstanceProfileName)
	}
}
