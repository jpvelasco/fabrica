package workstation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
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
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["GroupName"] != plan.SGName {
		t.Errorf("GroupName = %v, want %q", doc["GroupName"], plan.SGName)
	}
	if doc["VpcId"] != plan.VPCID {
		t.Errorf("VpcId = %v, want %q", doc["VpcId"], plan.VPCID)
	}
	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) == 0 {
		t.Fatal("SecurityGroupIngress missing or empty")
	}
	rule := ingress[0].(map[string]any)
	if rule["FromPort"] != float64(DefaultDCVPort) {
		t.Errorf("FromPort = %v, want %d", rule["FromPort"], DefaultDCVPort)
	}
}

func TestSGDesiredStateManagedByTag(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	if !containsStr(string(raw), "fabrica") {
		t.Error("SG desired state must contain ManagedBy=fabrica tag")
	}
}

func TestInstanceDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["ImageId"] != plan.AmiID {
		t.Errorf("ImageId = %v, want %q", doc["ImageId"], plan.AmiID)
	}
	if doc["InstanceType"] != plan.InstanceType {
		t.Errorf("InstanceType = %v, want %q", doc["InstanceType"], plan.InstanceType)
	}
	if doc["SubnetId"] != plan.SubnetID {
		t.Errorf("SubnetId = %v, want %q", doc["SubnetId"], plan.SubnetID)
	}
	sgs, ok := doc["SecurityGroupIds"].([]any)
	if !ok || len(sgs) != 1 || sgs[0] != "sg-test123" {
		t.Errorf("SecurityGroupIds = %v, want [sg-test123]", doc["SecurityGroupIds"])
	}
	meta, ok := doc["MetadataOptions"].(map[string]any)
	if !ok || meta["HttpTokens"] != "required" {
		t.Error("MetadataOptions.HttpTokens must be required (IMDSv2)")
	}
}

func TestInstanceDesiredStateVolume(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bdm, ok := doc["BlockDeviceMappings"].([]any)
	if !ok || len(bdm) == 0 {
		t.Fatal("BlockDeviceMappings missing")
	}
	ebs := bdm[0].(map[string]any)["Ebs"].(map[string]any)
	if ebs["VolumeSize"] != float64(plan.VolumeSize) {
		t.Errorf("VolumeSize = %v, want %d", ebs["VolumeSize"], plan.VolumeSize)
	}
	if ebs["VolumeType"] != "gp3" {
		t.Errorf("VolumeType = %v, want gp3", ebs["VolumeType"])
	}
}
