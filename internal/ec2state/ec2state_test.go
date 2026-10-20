package ec2state

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func testSpec() InstanceSpec {
	return InstanceSpec{
		ImageID:         "ami-123",
		InstanceType:    "m7i.xlarge",
		SubnetID:        "subnet-abc",
		SecurityGroupID: "sg-xyz",
		UserData:        "dGVzdA==",
		VolumeSize:      100,
		InstanceName:    "test-instance",
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
	raw, err := Build(testSpec())
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
	if meta["HttpPutResponseHopLimit"].(float64) != 1 {
		t.Errorf("HttpPutResponseHopLimit = %v, want 1", meta["HttpPutResponseHopLimit"])
	}
	if _, ok := meta["HopLimit"]; ok {
		t.Error("stale HopLimit key must not be emitted")
	}
}

// TestBuildDesiredStateJSONHasHttpPutResponseHopLimit pins the serialized
// Cloud Control property name: the JSON must carry
// "HttpPutResponseHopLimit":1, not the ignored "HopLimit" key, or Cloud
// Control applies the AWS default (5) instead of the pin.
func TestBuildDesiredStateJSONHasHttpPutResponseHopLimit(t *testing.T) {
	raw, err := Build(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"HttpPutResponseHopLimit":1`) {
		t.Errorf("desired-state JSON must contain \"HttpPutResponseHopLimit\":1, got: %s", raw)
	}
	if strings.Contains(string(raw), `"HopLimit"`) {
		t.Errorf("desired-state JSON must not contain the stale \"HopLimit\" key, got: %s", raw)
	}
}

func checkStr(t *testing.T, d map[string]any, key, want string) {
	t.Helper()
	if got := d[key].(string); got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

// imdsErrorRecorder captures AssertIMDSv2 errors without failing the test,
// so the in-package test can exercise the helper's error branches.
type imdsErrorRecorder struct {
	errs []string
}

func (r *imdsErrorRecorder) Helper() {}

func (r *imdsErrorRecorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestAssertIMDSv2AcceptsHopLimitOne(t *testing.T) {
	rec := &imdsErrorRecorder{}
	AssertIMDSv2(rec, map[string]any{
		"MetadataOptions": map[string]any{"HttpTokens": "required", "HttpPutResponseHopLimit": float64(1)},
	})
	if len(rec.errs) != 0 {
		t.Errorf("AssertIMDSv2 should pass for HttpTokens=required HttpPutResponseHopLimit=1, got %v", rec.errs)
	}
}

func TestAssertIMDSv2RejectsWrongHopLimit(t *testing.T) {
	rec := &imdsErrorRecorder{}
	AssertIMDSv2(rec, map[string]any{
		"MetadataOptions": map[string]any{"HttpTokens": "required", "HttpPutResponseHopLimit": float64(2)},
	})
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "HttpPutResponseHopLimit") {
		t.Errorf("AssertIMDSv2 should report the HttpPutResponseHopLimit mismatch, got %v", rec.errs)
	}
}

func TestAssertIMDSv2RejectsMissingMetadataOptions(t *testing.T) {
	rec := &imdsErrorRecorder{}
	AssertIMDSv2(rec, map[string]any{})
	if len(rec.errs) == 0 || !strings.Contains(rec.errs[0], "HttpTokens") {
		t.Errorf("AssertIMDSv2 should report missing MetadataOptions, got %v", rec.errs)
	}
}

func TestIAMProfile(t *testing.T) {
	raw, err := Build(testSpec(), WithIAMProfile("MyProfile"))
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	checkStr(t, d, "IamInstanceProfile", "MyProfile")
}

func TestNoIAMProfile(t *testing.T) {
	raw, err := Build(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	if _, ok := d["IamInstanceProfile"]; ok {
		t.Error("IamInstanceProfile should be absent")
	}
}

func TestDeviceName(t *testing.T) {
	raw, err := Build(testSpec(), WithDeviceName("/dev/sda1"))
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
	raw, err := Build(testSpec(), WithDeleteOnTermination(false))
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

// TestEncryptedVolume pins #507: WithEncryptedVolume sets Encrypted=true on
// the block device mapping, and Build without the option leaves the key out
// (the other modules keep their current request shape).
func TestEncryptedVolume(t *testing.T) {
	raw, err := Build(testSpec(), WithDeviceName("/dev/sda1"), WithEncryptedVolume())
	if err != nil {
		t.Fatal(err)
	}
	ebs := doc(t, raw)["BlockDeviceMappings"].([]any)[0].(map[string]any)["Ebs"].(map[string]any)
	if enc, ok := ebs["Encrypted"].(bool); !ok || !enc {
		t.Fatalf("Ebs.Encrypted = %v, want true", ebs["Encrypted"])
	}

	raw, err = Build(testSpec())
	if err != nil {
		t.Fatal(err)
	}
	ebs = doc(t, raw)["BlockDeviceMappings"].([]any)[0].(map[string]any)["Ebs"].(map[string]any)
	if _, ok := ebs["Encrypted"]; ok {
		t.Fatalf("Ebs.Encrypted present without WithEncryptedVolume: %v", ebs)
	}
}

// TestEncryptedVolumeNoMappings is a no-op on a document without mappings.
func TestEncryptedVolumeNoMappings(t *testing.T) {
	d := map[string]any{}
	WithEncryptedVolume()(d)
	if len(d) != 0 {
		t.Fatalf("doc = %v, want unchanged", d)
	}
}

func TestExtraTags(t *testing.T) {
	raw, err := Build(testSpec(), WithExtraTags("FabricaModule", "ddc"))
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
	raw, err := Build(testSpec(), WithIAMProfile("P4Profile"), WithDeleteOnTermination(false))
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
	raw, err := Build(testSpec(), WithDeviceName("/dev/sda1"))
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
	if _, ok := d["Path"]; ok {
		t.Errorf("Path = %v, want omitted", d["Path"])
	}
}

func TestInstanceProfileDesiredStatePath(t *testing.T) {
	raw, err := InstanceProfileDesiredState("my-profile", "my-role", ProfileOptions("/fabrica/")...)
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	checkStr(t, d, "Path", "/fabrica/")
	if _, ok := d["PermissionsBoundary"]; ok {
		t.Fatal("instance profile must not carry a permissions boundary")
	}
	if _, err := InstanceProfileDesiredState("my-profile", "my-role", WithProfilePath("fabrica")); err == nil {
		t.Fatal("expected invalid path to fail")
	}
	if _, err := InstanceProfileDesiredState("my-profile", "my-role", WithProfilePath("/fabrica//roles/")); err == nil {
		t.Fatal("expected empty path segment to fail")
	}
	if got := ProfileOptions(""); got != nil {
		t.Fatalf("empty profile options = %#v", got)
	}
}

func TestSGDesiredStateBasic(t *testing.T) {
	raw, err := SGDesiredState("my-sg", "My security group", "vpc-123", []SGIngressRule{
		{IpProtocol: "tcp", FromPort: 1666, ToPort: 1666, CidrIp: "10.0.0.0/8", Description: "p4d"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	checkStr(t, d, "GroupName", "my-sg")
	checkStr(t, d, "GroupDescription", "My security group")
	checkStr(t, d, "VpcId", "vpc-123")

	ingress := d["SecurityGroupIngress"].([]any)
	if len(ingress) != 1 {
		t.Fatalf("ingress len = %d, want 1", len(ingress))
	}
	rule := ingress[0].(map[string]any)
	if rule["IpProtocol"].(string) != "tcp" {
		t.Errorf("IpProtocol = %v", rule["IpProtocol"])
	}
	if rule["FromPort"].(float64) != 1666 {
		t.Errorf("FromPort = %v", rule["FromPort"])
	}
	if rule["Description"].(string) != "p4d" {
		t.Errorf("Description = %v", rule["Description"])
	}

	tags := d["Tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("Tags len = %d, want 2", len(tags))
	}
}

func TestSGDesiredStateExtraTags(t *testing.T) {
	raw, err := SGDesiredState("ddc-sg", "DDC SG", "vpc-123", []SGIngressRule{
		{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: "0.0.0.0/0", Description: "HTTP"},
	}, map[string]string{"FabricaModule": "ddc"})
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	tags := d["Tags"].([]any)
	if len(tags) != 3 {
		t.Fatalf("Tags len = %d, want 3", len(tags))
	}
	t2 := tags[2].(map[string]any)
	if t2["Key"].(string) != "FabricaModule" || t2["Value"].(string) != "ddc" {
		t.Errorf("extra tag = %v", t2)
	}
}

func TestBuildOmitsEmptyAMI(t *testing.T) {
	spec := testSpec()
	spec.ImageID = ""
	raw, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	d := doc(t, raw)
	if _, ok := d["ImageId"]; ok {
		t.Error("ImageId should be absent")
	}
}
