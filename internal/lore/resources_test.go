package lore

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func TestSGDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-lore-sg",
		VPCID:       "vpc-abc123",
		GRPCPort:    41337,
		HTTPPort:    41339,
		AllowedCIDR: "10.0.0.0/8",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["GroupName"] != "fabrica-lore-sg" {
		t.Errorf("GroupName = %v", doc["GroupName"])
	}

	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok {
		t.Fatalf("SecurityGroupIngress is not an array")
	}
	if len(ingress) != 3 {
		t.Fatalf("SecurityGroupIngress len = %d, want 3", len(ingress))
	}

	want := []struct {
		proto string
		port  float64
	}{
		{"tcp", 41337},
		{"udp", 41337},
		{"tcp", 41339},
	}
	for i, w := range want {
		r := ingress[i].(map[string]any)
		if r["IpProtocol"] != w.proto {
			t.Errorf("ingress[%d].IpProtocol = %v, want %s", i, r["IpProtocol"], w.proto)
		}
		if r["FromPort"] != w.port || r["ToPort"] != w.port {
			t.Errorf("ingress[%d] ports = %v/%v, want %v", i, r["FromPort"], r["ToPort"], w.port)
		}
		if r["CidrIp"] != "10.0.0.0/8" {
			t.Errorf("ingress[%d].CidrIp = %v", i, r["CidrIp"])
		}
	}

	// Ensure UDP rule is present (first Fabrica module with UDP).
	var sawUDP bool
	for _, rule := range ingress {
		r := rule.(map[string]any)
		if r["IpProtocol"] == "udp" {
			sawUDP = true
		}
	}
	if !sawUDP {
		t.Error("expected a UDP ingress rule for QUIC")
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestInstanceDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		AmiID:        "ami-lore1",
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-1",
		VolumeSize:   500,
		InstanceName: "fabrica-lore",
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	ec2state.AssertStringField(t, doc, "ImageId", "ami-lore1")
	ec2state.AssertStringField(t, doc, "InstanceType", "m5.xlarge")
	ec2state.AssertSGID(t, doc, "sg-abc")

	bdm := doc["BlockDeviceMappings"].([]any)
	ebs := bdm[0].(map[string]any)["Ebs"].(map[string]any)
	if ebs["VolumeSize"] != float64(500) {
		t.Errorf("VolumeSize = %v", ebs["VolumeSize"])
	}
	if ebs["DeleteOnTermination"] != true {
		t.Errorf("DeleteOnTermination = %v, want true (destroy deletes store with instance)", ebs["DeleteOnTermination"])
	}
}
