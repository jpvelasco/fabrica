package ec2plan

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestNewHappyPath(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-123", SubnetID: "subnet-456"}
	base, err := New(context.Background(), Params{
		Account:      "123456789012",
		Region:       "us-east-1",
		ModuleName:   "horde",
		InstanceType: "m7i.2xlarge",
		VolumeSize:   100,
		AllowedCIDR:  "10.0.0.0/8",
		VPCId:        "",
		SubnetId:     "",
		Resolver:     resolver,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if base.Account != "123456789012" {
		t.Errorf("Account = %q, want %q", base.Account, "123456789012")
	}
	if base.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", base.Region, "us-east-1")
	}
	if base.InstanceType != "m7i.2xlarge" {
		t.Errorf("InstanceType = %q, want %q", base.InstanceType, "m7i.2xlarge")
	}
	if base.VolumeSize != 100 {
		t.Errorf("VolumeSize = %d, want %d", base.VolumeSize, 100)
	}
	if base.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want %q", base.AllowedCIDR, "10.0.0.0/8")
	}
	if base.VPCID != "vpc-123" {
		t.Errorf("VPCID = %q, want %q", base.VPCID, "vpc-123")
	}
	if base.SubnetID != "subnet-456" {
		t.Errorf("SubnetID = %q, want %q", base.SubnetID, "subnet-456")
	}
	if !base.DefaultVPC {
		t.Error("DefaultVPC = false, want true (resolved via resolver)")
	}
	if base.SGName != "fabrica-horde-sg" {
		t.Errorf("SGName = %q, want %q", base.SGName, "fabrica-horde-sg")
	}
	if base.InstanceName != "fabrica-horde" {
		t.Errorf("InstanceName = %q, want %q", base.InstanceName, "fabrica-horde")
	}
	if resolver.Calls != 1 {
		t.Errorf("resolver.Calls = %d, want 1", resolver.Calls)
	}
}

func TestNewExplicitVPC(t *testing.T) {
	base, err := New(context.Background(), Params{
		Account:      "123456789012",
		Region:       "us-east-1",
		ModuleName:   "perforce",
		InstanceType: "m5.xlarge",
		VolumeSize:   500,
		AllowedCIDR:  "10.0.0.0/8",
		VPCId:        "vpc-explicit",
		SubnetId:     "subnet-explicit",
		Resolver:     nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want %q", base.VPCID, "vpc-explicit")
	}
	if base.SubnetID != "subnet-explicit" {
		t.Errorf("SubnetID = %q, want %q", base.SubnetID, "subnet-explicit")
	}
	if base.DefaultVPC {
		t.Error("DefaultVPC = true, want false (explicit VPC provided)")
	}
	if base.SGName != "fabrica-perforce-sg" {
		t.Errorf("SGName = %q, want %q", base.SGName, "fabrica-perforce-sg")
	}
}

func TestNewResolverError(t *testing.T) {
	resolver := &cloud.TestVPCResolver{Err: assertAnError{msg: "no VPC"}}
	_, err := New(context.Background(), Params{
		Account:    "123456789012",
		Region:     "us-east-1",
		ModuleName: "lore",
		VPCId:      "",
		SubnetId:   "",
		Resolver:   resolver,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNewNilResolverExplicitVPC(t *testing.T) {
	// Nil resolver is OK when VPC is explicit.
	base, err := New(context.Background(), Params{
		Account:    "123456789012",
		Region:     "us-east-1",
		ModuleName: "workstation",
		VPCId:      "vpc-abc",
		SubnetId:   "subnet-abc",
		Resolver:   nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.VPCID != "vpc-abc" || base.SubnetID != "subnet-abc" {
		t.Errorf("VPC/Subnet not preserved with nil resolver")
	}
}

func TestStringOr(t *testing.T) {
	tests := []struct {
		name, value, fallback, want string
	}{
		{"non-empty", "hello", "world", "hello"},
		{"empty", "", "world", "world"},
		{"both set", "hello", "world", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringOr(tt.value, tt.fallback); got != tt.want {
				t.Errorf("StringOr(%q, %q) = %q, want %q", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestIntOrPositive(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		fallback int
		want     int
	}{
		{"positive", 100, 50, 100},
		{"zero", 0, 50, 50},
		{"negative", -1, 50, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IntOrPositive(tt.value, tt.fallback); got != tt.want {
				t.Errorf("IntOrPositive(%d, %d) = %d, want %d", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
}

type assertAnError struct{ msg string }

func (e assertAnError) Error() string { return e.msg }
