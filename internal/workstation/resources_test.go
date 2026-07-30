package workstation

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func testPlan(t *testing.T) *CreatePlan {
	t.Helper()
	plan, err := NewCreatePlan(context.Background(), config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-test",
		SubnetId: "subnet-test",
	}, "123456789012", "us-east-1", nil, "", "")
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	return plan
}

func TestSGDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "GroupName", plan.SGName)
	ec2state.AssertStringField(t, doc, "VpcId", plan.VPCID)
	ec2state.AssertIngressCidr(t, doc, 1, plan.AllowedCIDR)
}

func TestSGDesiredStateManagedByTag(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	ec2state.AssertManagedByTag(t, raw)
}

func TestInstanceDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "ImageId", plan.AmiID)
	ec2state.AssertStringField(t, doc, "InstanceType", plan.InstanceType)
	ec2state.AssertStringField(t, doc, "SubnetId", plan.SubnetID)
	ec2state.AssertSGID(t, doc, "sg-test123")
	ec2state.AssertIMDSv2(t, doc)
}

func TestInstanceDesiredStateVolume(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertEBS(t, doc, plan.VolumeSize, true)
}
