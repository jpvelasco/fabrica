package perforce

import (
	"context"
	"testing"
)

func TestNewCreatePlan_Defaults(t *testing.T) {
	cfg := PerforceConfig{}
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
}

func TestNewCreatePlan_CustomInstanceType(t *testing.T) {
	cfg := PerforceConfig{InstanceType: "c5.2xlarge"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "c5.2xlarge" {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, "c5.2xlarge")
	}
}

func TestNewCreatePlan_CostResourceTypeNames(t *testing.T) {
	cfg := PerforceConfig{}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	typeNames := make(map[string]bool)
	for _, r := range plan.CostResources {
		typeNames[r.TypeName] = true
	}
	for _, want := range []string{"AWS::EC2::Instance", "AWS::EC2::Volume"} {
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
			_, err := NewCreatePlan(context.Background(), PerforceConfig{}, "123456789012", "us-east-1", tc.version, nil)
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
	cfg := PerforceConfig{}
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
	cfg := PerforceConfig{VPCId: "vpc-explicit", SubnetId: "subnet-explicit"}
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

type fakeVPCResolver struct {
	vpcID    string
	subnetID string
	err      error
}

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return f.vpcID, f.subnetID, f.err
}
