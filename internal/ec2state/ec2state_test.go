package ec2state

import (
	"encoding/json"
	"testing"
)

func optsAll() []InstanceOption {
	return []InstanceOption{
		WithAMI("ami-123"),
		WithInstanceType("m7i.xlarge"),
		WithSubnet("subnet-abc"),
		WithSecurityGroup("sg-xyz"),
		WithUserData("dGVzdA=="),
		WithVolumeSize(100),
		WithInstanceName("test-instance"),
	}
}

func doc(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return d
}

func TestBuildCoreFields(t *testing.T) {
	raw, err := Build(optsAll())
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)

	checkStr(t, d, "ImageId", "ami-123")
	checkStr(t, d, "InstanceType", "m7i.xlarge")
	checkStr(t, d, "SubnetId", "subnet-abc")
	checkStr(t, d, "UserData", "dGVzdA==")

	sgs := d["SecurityGroupIds"].([]any)
	if len(sgs) != 1 || sgs[0].(string) != "sg-xyz" {
		t.Errorf("SecurityGroupIds = %v", sgs)
	}

	// Block device mappings.
	mappings := d["BlockDeviceMappings"].([]any)
	m0 := mappings[0].(map[string]any)
	if m0["DeviceName"].(string) != "/dev/sdf" {
		t.Errorf("DeviceName = %v", m0["DeviceName"])
	}
	ebs := m0["Ebs"].(map[string]any)
	if ebs["VolumeSize"].(float64) != 100 {
		t.Errorf("VolumeSize = %v", ebs["VolumeSize"])
	}
	if ebs["VolumeType"].(string) != "gp3" {
		t.Errorf("VolumeType = %v", ebs["VolumeType"])
	}
	if ebs["DeleteOnTermination"].(bool) != true {
		t.Error("DeleteOnTermination should default to true")
	}

	// Tags.
	tags := d["Tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(tags))
	}
	t0 := tags[0].(map[string]any)
	if t0["Key"].(string) != "ManagedBy" || t0["Value"].(string) != "fabrica" {
		t.Errorf("ManagedBy tag = %v", t0)
	}
	t1 := tags[1].(map[string]any)
	if t1["Key"].(string) != "Name" || t1["Value"].(string) != "test-instance" {
		t.Errorf("Name tag = %v", t1)
	}

	// Metadata options.
	meta := d["MetadataOptions"].(map[string]any)
	if meta["HttpTokens"].(string) != "required" {
		t.Errorf("HttpTokens = %v", meta["HttpTokens"])
	}
}

func checkStr(t *testing.T, d map[string]any, key, want string) {
	t.Helper()
	if got := d[key].(string); got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func TestIAMProfile(t *testing.T) {
	raw, err := Build(optsAll(), WithIAMProfile("MyProfile"))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	checkStr(t, d, "IamInstanceProfile", "MyProfile")
}

func TestNoIAMProfile(t *testing.T) {
	raw, err := Build(optsAll())
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	if _, ok := d["IamInstanceProfile"]; ok {
		t.Error("IamInstanceProfile should be absent")
	}
}

func TestDeviceName(t *testing.T) {
	raw, err := Build(optsAll(), WithDeviceName("/dev/sda1"))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	mappings := d["BlockDeviceMappings"].([]any)
	m0 := mappings[0].(map[string]any)
	if m0["DeviceName"].(string) != "/dev/sda1" {
		t.Errorf("DeviceName = %v", m0["DeviceName"])
	}
}

func TestDeleteOnTerminationFalse(t *testing.T) {
	raw, err := Build(optsAll(), WithDeleteOnTermination(false))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	mappings := d["BlockDeviceMappings"].([]any)
	m0 := mappings[0].(map[string]any)
	ebs := m0["Ebs"].(map[string]any)
	if ebs["DeleteOnTermination"].(bool) {
		t.Error("DeleteOnTermination should be false")
	}
}

func TestExtraTags(t *testing.T) {
	raw, err := Build(optsAll(), WithExtraTags("FabricaModule", "ddc"))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	tags := d["Tags"].([]any)
	if len(tags) != 3 {
		t.Fatalf("Tags len = %d, want 3", len(tags))
	}
	t2 := tags[2].(map[string]any)
	if t2["Key"].(string) != "FabricaModule" {
		t.Error("extra tag key")
	}
	if t2["Value"].(string) != "ddc" {
		t.Error("extra tag value")
	}
}

func TestPerforceShape(t *testing.T) {
	// Perforce: /dev/sdf, DeleteOnTermination=false, IAM profile.
	raw, err := Build(optsAll(), WithIAMProfile("P4Profile"), WithDeleteOnTermination(false))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	mappings := d["BlockDeviceMappings"].([]any)
	m0 := mappings[0].(map[string]any)
	if m0["DeviceName"].(string) != "/dev/sdf" {
		t.Error("device name should be /dev/sdf")
	}
	ebs := m0["Ebs"].(map[string]any)
	if ebs["DeleteOnTermination"].(bool) {
		t.Error("DeleteOnTermination should be false")
	}
	checkStr(t, d, "IamInstanceProfile", "P4Profile")
}

func TestWorkstationShape(t *testing.T) {
	// Workstation: /dev/sda1, DeleteOnTermination=true (default), no IAM.
	raw, err := Build(optsAll(), WithDeviceName("/dev/sda1"))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	mappings := d["BlockDeviceMappings"].([]any)
	m0 := mappings[0].(map[string]any)
	if m0["DeviceName"].(string) != "/dev/sda1" {
		t.Error("device name should be /dev/sda1")
	}
	if _, ok := d["IamInstanceProfile"]; ok {
		t.Error("IamInstanceProfile should be absent")
	}
}

func TestInstanceProfileDesiredState(t *testing.T) {
	raw, err := InstanceProfileDesiredState("my-profile", "my-role")
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	checkStr(t, d, "InstanceProfileName", "my-profile")
	roles := d["Roles"].([]any)
	if len(roles) != 1 || roles[0].(string) != "my-role" {
		t.Errorf("Roles = %v, want [my-role]", roles)
	}
}

func TestUserDataRaw(t *testing.T) {
	opts := []InstanceOption{
		WithAMI("ami-123"),
		WithInstanceType("m7i.xlarge"),
		WithSubnet("subnet-abc"),
		WithSecurityGroup("sg-xyz"),
		WithUserDataRaw("#!/bin/bash\necho hi"),
		WithVolumeSize(50),
		WithInstanceName("test"),
	}
	raw, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	if d["UserData"].(string) != "IyEvYmluL2Jhc2gKZWNobyBoaQ==" {
		t.Errorf("raw user data = %v", d["UserData"])
	}
}
