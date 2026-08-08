package ddc

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestNewEdgePlanDefaults(t *testing.T) {
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID:    "ami-ddc",
		VPCId:    "vpc-home",
		SubnetId: "subnet-home",
	}, "123456789012", "us-east-1", "eu-west-1", EdgeOptions{VPCID: "vpc-edge", SubnetID: "subnet-edge"}, nil)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.Region != "eu-west-1" {
		t.Fatalf("Region = %q", plan.Region)
	}
	if plan.AmiID != "ami-ddc" {
		t.Fatalf("AmiID = %q", plan.AmiID)
	}
	if plan.InstanceType != DefaultInstanceType || plan.VolumeSize != DefaultVolumeSize {
		t.Fatalf("defaults: %s %d", plan.InstanceType, plan.VolumeSize)
	}
	// Edge shares the home blob bucket, not a region-specific one.
	if plan.Bucket != "fabrica-ddc-123456789012-us-east-1" {
		t.Fatalf("Bucket = %q", plan.Bucket)
	}
	if plan.SGName != "fabrica-ddc-sg-eu-west-1" || plan.InstanceName != "fabrica-ddc-edge-eu-west-1" {
		t.Fatalf("names: %s %s", plan.SGName, plan.InstanceName)
	}
	if plan.InstanceProfileName != "fabrica-ddc-profile" {
		t.Fatalf("profile = %q", plan.InstanceProfileName)
	}
	if plan.PublicPort != DefaultPublicPort || plan.AllowedCIDR != DefaultAllowedCIDR {
		t.Fatalf("ports/cidr: %d %s", plan.PublicPort, plan.AllowedCIDR)
	}
	if plan.VPCID != "vpc-edge" || plan.SubnetID != "subnet-edge" {
		t.Fatalf("vpc: %s %s", plan.VPCID, plan.SubnetID)
	}
	if plan.DefaultVPC {
		t.Fatal("DefaultVPC should be false when flags are set")
	}
	if len(plan.CostResources) != 2 {
		t.Fatalf("cost resources = %d", len(plan.CostResources))
	}
}

func TestNewEdgePlanOverrides(t *testing.T) {
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID:        "ami-home",
		InstanceType: "m7i.xlarge",
		VolumeSize:   500,
		VPCId:        "vpc-home",
		SubnetId:     "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{
		AmiID:        "ami-edge",
		InstanceType: "m7i.large",
		VolumeSize:   250,
		VPCID:        "vpc-eu",
		SubnetID:     "subnet-eu",
	}, nil)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.AmiID != "ami-edge" || plan.InstanceType != "m7i.large" || plan.VolumeSize != 250 {
		t.Fatalf("overrides: %s %s %d", plan.AmiID, plan.InstanceType, plan.VolumeSize)
	}
	if plan.VPCID != "vpc-eu" {
		t.Fatalf("VPCID = %q", plan.VPCID)
	}
}

func TestNewEdgePlanResolvesVPCFromResolver(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-eu", SubnetID: "subnet-eu"}
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID: "ami-ddc", VPCId: "vpc-home", SubnetId: "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{}, resolver)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.VPCID != "vpc-eu" || plan.SubnetID != "subnet-eu" {
		t.Fatalf("vpc: %s %s", plan.VPCID, plan.SubnetID)
	}
	if !plan.DefaultVPC {
		t.Fatal("DefaultVPC should be true when resolved")
	}
	if resolver.Calls != 1 {
		t.Fatalf("resolver calls = %d", resolver.Calls)
	}
}

func TestNewEdgePlanSkipsResolverWhenFlagsSet(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-other"}
	_, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID: "ami-ddc", VPCId: "vpc-home", SubnetId: "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{VPCID: "vpc-set", SubnetID: "subnet-set"}, resolver)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if resolver.Calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.Calls)
	}
}

func TestNewEdgePlanValidation(t *testing.T) {
	cfg := config.DDCConfig{AmiID: "ami-ddc", VPCId: "v", SubnetId: "s"}
	cases := []struct {
		name       string
		home       string
		region     string
		wantSubstr string
	}{
		{name: "empty region", home: "us-east-1", region: "", wantSubstr: "region is required"},
		{name: "bad region format", home: "us-east-1", region: "US_EAST_1", wantSubstr: "not a valid AWS region name"},
		{name: "home region", home: "us-east-1", region: "us-east-1", wantSubstr: "is the home region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewEdgePlan(context.Background(), cfg, "1", tc.home, tc.region, EdgeOptions{}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err = %v, want substr %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestNewEdgePlanRequiresAmi(t *testing.T) {
	_, err := NewEdgePlan(context.Background(), config.DDCConfig{}, "1", "us-east-1", "eu-west-1", EdgeOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "--ami-id") {
		t.Fatalf("err = %v", err)
	}
}

func TestEdgeCostResources(t *testing.T) {
	out := EdgeCostResources(config.DDCConfig{}, EdgeOptions{})
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Name != DefaultInstanceType {
		t.Fatalf("instance = %q", out[0].Name)
	}
	if out[1].Name != "gp3-500GiB" {
		t.Fatalf("volume = %q", out[1].Name)
	}
	withOpts := EdgeCostResources(config.DDCConfig{InstanceType: "m7i.large", VolumeSize: 250}, EdgeOptions{InstanceType: "c7i.large", VolumeSize: 100})
	if withOpts[0].Name != "c7i.large" || withOpts[1].Name != "gp3-100GiB" {
		t.Fatalf("opts override: %+v", withOpts)
	}
	withCfg := EdgeCostResources(config.DDCConfig{InstanceType: "m7i.large", VolumeSize: 250}, EdgeOptions{})
	if withCfg[0].Name != "m7i.large" || withCfg[1].Name != "gp3-250GiB" {
		t.Fatalf("config fallback: %+v", withCfg)
	}
}

func TestEdgeRegions(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home", Properties: map[string]string{"region": "us-east-1"}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home", Properties: map[string]string{"region": "us-east-1", "role": RoleCoordinator}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{
			"region": "eu-west-1", "role": RoleEdge, "instanceType": "m7i.large", "volumeSize": "250",
		}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": RoleEdge}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": RoleEdge}},
	}
	edges := EdgeRegions(resources, "us-east-1")
	if len(edges) != 2 {
		t.Fatalf("len = %d, want 2", len(edges))
	}
	if edges[0].Region != "ap-southeast-2" || edges[1].Region != "eu-west-1" {
		t.Fatalf("order = %s, %s", edges[0].Region, edges[1].Region)
	}
	if edges[1].InstanceID != "i-eu" || edges[1].SGID != "sg-eu" {
		t.Fatalf("eu edge = %+v", edges[1])
	}
	if edges[1].InstanceType != "m7i.large" || edges[1].VolumeSize != 250 {
		t.Fatalf("eu shape = %+v", edges[1])
	}
}

func TestEdgeRegionsNoEdges(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home", Properties: map[string]string{"region": "us-east-1", "role": RoleCoordinator}},
	}
	if got := EdgeRegions(resources, "us-east-1"); len(got) != 0 {
		t.Fatalf("got %d edges, want 0", len(got))
	}
}

func TestEdgeRegionsSkipsHomeEdge(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home-edge", Properties: map[string]string{"region": "us-east-1", "role": RoleEdge}},
	}
	if got := EdgeRegions(resources, "us-east-1"); len(got) != 0 {
		t.Fatalf("home co-located edge must be excluded, got %+v", got)
	}
}

func TestEdgeExistsAndInstance(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge}},
	}
	if EdgeExists(resources, "eu-west-1") != true {
		t.Fatal("EdgeExists should be true (SG counts)")
	}
	if EdgeExists(resources, "us-west-2") {
		t.Fatal("EdgeExists should be false for other region")
	}
	if EdgeInstanceExists(resources, "eu-west-1") {
		t.Fatal("EdgeInstanceExists should be false (no instance yet)")
	}
	resources = append(resources, state.ModuleResource{
		TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge},
	})
	if !EdgeInstanceExists(resources, "eu-west-1") {
		t.Fatal("EdgeInstanceExists should be true")
	}
}

func TestEdgePlanPropertyHelpers(t *testing.T) {
	if property(state.ModuleResource{}, "nope") != "" {
		t.Fatal("expected empty property")
	}
	if atoiOrZero("") != 0 || atoiOrZero("abc") != 0 || atoiOrZero("42") != 42 {
		t.Fatal("atoiOrZero mishandled input")
	}
}
