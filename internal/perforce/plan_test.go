package perforce

import (
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlan_Defaults(t *testing.T) {
	cfg := config.PerforceConfig{}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.SGName != "fabrica-perforce-sg" {
		t.Errorf("SGName = %q, want %q", plan.SGName, "fabrica-perforce-sg")
	}
	if plan.InstanceName != "fabrica-perforce" {
		t.Errorf("InstanceName = %q, want %q", plan.InstanceName, "fabrica-perforce")
	}
	if plan.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, "m5.xlarge")
	}
	if plan.VolumeSize != 500 {
		t.Errorf("VolumeSize = %d, want 500", plan.VolumeSize)
	}
	if plan.HelixVersion != DefaultHelixVersion {
		t.Errorf("HelixVersion = %q, want %q", plan.HelixVersion, DefaultHelixVersion)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
}

func TestNewCreatePlan_AllowedCIDR(t *testing.T) {
	cases := []struct {
		name string
		cidr string
		want string
	}{
		{"default empty", "", "10.0.0.0/8"},
		{"explicit vpc", "172.16.0.0/12", "172.16.0.0/12"},
		{"open internet", "0.0.0.0/0", "0.0.0.0/0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.PerforceConfig{AllowedCIDR: tc.cidr}
			plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.AllowedCIDR != tc.want {
				t.Errorf("AllowedCIDR = %q, want %q", plan.AllowedCIDR, tc.want)
			}
		})
	}
}

func TestNewCreatePlan_CustomInstanceType(t *testing.T) {
	cfg := config.PerforceConfig{InstanceType: "c5.2xlarge"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "c5.2xlarge" {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, "c5.2xlarge")
	}
}

func TestNewCreatePlan_CostResourceTypeNames(t *testing.T) {
	cfg := config.PerforceConfig{}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typeNames := make(map[string]bool)
	for _, r := range plan.CostResources {
		typeNames[r.TypeName] = true
	}
	for _, want := range []string{cloud.TypeAWSEC2Instance, cloud.TypeAWSEC2Volume} {
		if !typeNames[want] {
			t.Errorf("CostResources missing TypeName %q", want)
		}
	}
}

func TestNewCreatePlan_VersionValidation(t *testing.T) {
	cases := []struct {
		version string
		wantErr bool
	}{
		{"latest", false},
		{"2024.2", false},
		{"2024.2/2659294", false},
		{"2025.1", false},
		{"2025.1/1234567", false},
		{"bad", true},
		{"", true},
		{"2024", true},
		{"24.2", true},
		{"2024.2.3", true},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			_, err := NewCreatePlan(context.Background(), config.PerforceConfig{}, "123456789012", "us-east-1", tc.version, nil)
			if tc.wantErr && err == nil {
				t.Errorf("version %q: expected error, got nil", tc.version)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("version %q: unexpected error: %v", tc.version, err)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	cases := []struct {
		flag, cfg, want string
	}{
		{"2024.2", "2023.1", "2024.2"},
		{"", "2023.1", "2023.1"},
		{"", "", DefaultHelixVersion},
		{"latest", "2023.1", "latest"},
	}
	for _, tc := range cases {
		got := ResolveVersion(tc.flag, tc.cfg)
		if got != tc.want {
			t.Errorf("ResolveVersion(%q, %q) = %q, want %q", tc.flag, tc.cfg, got, tc.want)
		}
	}
}

func TestNewCreatePlan_VPCResolver(t *testing.T) {
	resolver := &fakeVPCResolver{vpcID: "vpc-abc", subnetID: "subnet-abc"}
	cfg := config.PerforceConfig{}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-abc" {
		t.Errorf("VPCID = %q, want %q", plan.VPCID, "vpc-abc")
	}
	if plan.SubnetID != "subnet-abc" {
		t.Errorf("SubnetID = %q, want %q", plan.SubnetID, "subnet-abc")
	}
	if !plan.DefaultVPC {
		t.Error("DefaultVPC should be true when resolver is used")
	}
}

func TestNewCreatePlan_ExplicitVPC(t *testing.T) {
	cfg := config.PerforceConfig{VPCId: "vpc-explicit", SubnetId: "subnet-explicit"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want %q", plan.VPCID, "vpc-explicit")
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC is explicitly configured")
	}
}

func TestNewCreatePlan_VolumeSize(t *testing.T) {
	cases := []struct {
		cfgVolume  int
		wantVolume int
	}{
		{0, 500},     // default
		{-1, 500},    // negative treated as zero → default
		{100, 100},   // explicit small
		{2000, 2000}, // explicit large
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("cfg=%d", tc.cfgVolume), func(t *testing.T) {
			cfg := config.PerforceConfig{VolumeSize: tc.cfgVolume}
			plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.VolumeSize != tc.wantVolume {
				t.Errorf("VolumeSize = %d, want %d", plan.VolumeSize, tc.wantVolume)
			}
		})
	}
}

func TestNewCreatePlan_VPCResolverError(t *testing.T) {
	resolver := &fakeVPCResolver{err: fmt.Errorf("no default VPC")}
	cfg := config.PerforceConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, resolver)
	if err == nil {
		t.Fatal("expected error when VPC resolver fails")
	}
	if !containsString(err.Error(), "resolving default VPC") {
		t.Errorf("error %q should mention resolving default VPC", err.Error())
	}
}

func TestNewCreatePlan_ExplicitVPCSkipsResolver(t *testing.T) {
	called := false
	resolver := &callTrackingResolver{onCall: func() { called = true }, vpcID: "vpc-skip", subnetID: "subnet-skip"}
	cfg := config.PerforceConfig{VPCId: "vpc-explicit", SubnetId: "subnet-explicit"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("resolver was called despite explicit VPC config — should be skipped")
	}
	if plan.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want vpc-explicit", plan.VPCID)
	}
}

func TestNewCreatePlan_AccountAndRegionPropagated(t *testing.T) {
	cfg := config.PerforceConfig{}
	plan, err := NewCreatePlan(context.Background(), cfg, "999999999999", "eu-west-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Account != "999999999999" {
		t.Errorf("Account = %q, want 999999999999", plan.Account)
	}
	if plan.Region != "eu-west-1" {
		t.Errorf("Region = %q, want eu-west-1", plan.Region)
	}
}

func TestNewCreatePlan_CostResourceNamesReflectInputs(t *testing.T) {
	cfg := config.PerforceConfig{InstanceType: "c5.2xlarge", VolumeSize: 1000}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var foundInstance, foundVolume bool
	for _, r := range plan.CostResources {
		if r.TypeName == cloud.TypeAWSEC2Instance && r.Name == "c5.2xlarge" {
			foundInstance = true
		}
		if r.TypeName == cloud.TypeAWSEC2Volume && r.Name == "gp3-1000GiB" {
			foundVolume = true
		}
	}
	if !foundInstance {
		t.Error("CostResources missing AWS::EC2::Instance with name c5.2xlarge")
	}
	if !foundVolume {
		t.Error("CostResources missing AWS::EC2::Volume with name gp3-1000GiB")
	}
}

func containsString(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}

type fakeVPCResolver struct {
	vpcID    string
	subnetID string
	err      error
}

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return f.vpcID, f.subnetID, f.err
}

type callTrackingResolver struct {
	onCall   func()
	vpcID    string
	subnetID string
}

func (r *callTrackingResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	r.onCall()
	return r.vpcID, r.subnetID, nil
}
