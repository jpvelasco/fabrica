package horde

import (
	"encoding/json"
	"testing"
)

func parseTags(t *testing.T, raw []any) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tag is not a map: %T", item)
		}
		k, _ := m["Key"].(string)
		v, _ := m["Value"].(string)
		result[k] = v
	}
	return result
}

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
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("GroupName = %v, want fabrica-horde-sg", doc["GroupName"])
	}
	if doc["VpcId"] != "vpc-abc123" {
		t.Errorf("VpcId = %v, want vpc-abc123", doc["VpcId"])
	}
	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok {
		t.Fatalf("SecurityGroupIngress is not an array")
	}
	if len(ingress) != 2 {
		t.Fatalf("SecurityGroupIngress len = %d, want 2", len(ingress))
	}
	ports := []float64{5000, 5002}
	for i, rule := range ingress {
		r := rule.(map[string]any)
		if r["CidrIp"] != "10.0.0.0/8" {
			t.Errorf("ingress[%d].CidrIp = %v, want 10.0.0.0/8", i, r["CidrIp"])
		}
		if r["FromPort"] != ports[i] {
			t.Errorf("ingress[%d].FromPort = %v, want %v", i, r["FromPort"], ports[i])
		}
	}
	tags := parseTags(t, doc["Tags"].([]any))
	if tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tags["ManagedBy"])
	}
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
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshalling SGDesiredState: %v", err)
	}
	ingress := doc["SecurityGroupIngress"].([]any)
	for i, rule := range ingress {
		r := rule.(map[string]any)
		if r["CidrIp"] != "172.16.0.0/12" {
			t.Errorf("ingress[%d].CidrIp = %v, want 172.16.0.0/12", i, r["CidrIp"])
		}
	}
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
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["ImageId"] != "ami-abc123" {
		t.Errorf("ImageId = %v, want ami-abc123", doc["ImageId"])
	}
	if doc["InstanceType"] != "m7i.xlarge" {
		t.Errorf("InstanceType = %v, want m7i.xlarge", doc["InstanceType"])
	}
	if doc["SubnetId"] != "subnet-abc" {
		t.Errorf("SubnetId = %v, want subnet-abc", doc["SubnetId"])
	}
	if doc["UserData"] != "dXNlcmRhdGE=" {
		t.Errorf("UserData = %v, want dXNlcmRhdGE=", doc["UserData"])
	}
	sgIDs, ok := doc["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0] != "sg-abc123" {
		t.Errorf("SecurityGroupIds = %v, want [sg-abc123]", doc["SecurityGroupIds"])
	}
	meta, ok := doc["MetadataOptions"].(map[string]any)
	if !ok || meta["HttpTokens"] != "required" {
		t.Errorf("MetadataOptions.HttpTokens not 'required': %v", doc["MetadataOptions"])
	}
	bdms, ok := doc["BlockDeviceMappings"].([]any)
	if !ok || len(bdms) != 1 {
		t.Fatalf("BlockDeviceMappings = %v, want 1 entry", doc["BlockDeviceMappings"])
	}
	bdm := bdms[0].(map[string]any)
	ebs := bdm["Ebs"].(map[string]any)
	if ebs["VolumeType"] != "gp3" {
		t.Errorf("EBS VolumeType = %v, want gp3", ebs["VolumeType"])
	}
	if ebs["VolumeSize"] != float64(100) {
		t.Errorf("EBS VolumeSize = %v, want 100", ebs["VolumeSize"])
	}
	if ebs["DeleteOnTermination"] != false {
		t.Errorf("EBS DeleteOnTermination = %v, want false", ebs["DeleteOnTermination"])
	}
	tags := parseTags(t, doc["Tags"].([]any))
	if tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tags["ManagedBy"])
	}
	if tags["Name"] != "fabrica-horde" {
		t.Errorf("Name tag = %q, want fabrica-horde", tags["Name"])
	}
}
