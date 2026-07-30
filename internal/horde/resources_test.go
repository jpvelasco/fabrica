package horde

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func TestSGDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-horde-sg",
		VPCID:       "vpc-abc123",
		Port:        5000,
		GRPCPort:    5002,
		AllowedCIDR: "10.0.0.0/8",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("GroupName = %v, want fabrica-horde-sg", doc["GroupName"])
	}
	if doc["VpcId"] != "vpc-abc123" {
		t.Errorf("VpcId = %v, want vpc-abc123", doc["VpcId"])
	}

	ec2state.AssertIngressCidr(t, doc, 2, "10.0.0.0/8")
	ingress := doc["SecurityGroupIngress"].([]any)
	ports := []float64{5000, 5002}
	for i, rule := range ingress {
		r := rule.(map[string]any)
		if r["FromPort"] != ports[i] {
			t.Errorf("ingress[%d].FromPort = %v, want %v", i, r["FromPort"], ports[i])
		}
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestSGDesiredStateAllowedCIDRAppliedToBothPorts(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-horde-sg",
		VPCID:       "vpc-abc123",
		Port:        5000,
		GRPCPort:    5002,
		AllowedCIDR: "172.16.0.0/12",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertIngressCidr(t, doc, 2, "172.16.0.0/12")
}

func TestInstanceDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		InstanceName: "fabrica-horde",
		InstanceType: "m7i.xlarge",
		AmiID:        "ami-abc123",
		SubnetID:     "subnet-abc",
		VolumeSize:   100,
	}
	raw, err := InstanceDesiredState(plan, "sg-abc123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	ec2state.AssertStringField(t, doc, "ImageId", "ami-abc123")
	ec2state.AssertStringField(t, doc, "InstanceType", "m7i.xlarge")
	ec2state.AssertStringField(t, doc, "SubnetId", "subnet-abc")
	ec2state.AssertStringField(t, doc, "UserData", "dXNlcmRhdGE=")
	ec2state.AssertSGID(t, doc, "sg-abc123")
	ec2state.AssertIMDSv2(t, doc)
	ec2state.AssertEBS(t, doc, 100, false)

	tags := ec2state.ParseTags(t, doc["Tags"].([]any))
	if tags["Name"] != "fabrica-horde" {
		t.Errorf("Name tag = %q, want fabrica-horde", tags["Name"])
	}
}
