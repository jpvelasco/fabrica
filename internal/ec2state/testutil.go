// Package ec2state provides shared test utilities for EC2 resource tests.
// Shared by horde, lore, perforce, workstation resource_test.go to eliminate
// the parseTags / unmarshal / field-assertion pattern duplicated across modules.
package ec2state

import (
	"encoding/json"
	"testing"
)

// ParseTags converts the Tags array from a Cloud Control desired-state document
// into a flat string map for easy assertion.
func ParseTags(t *testing.T, raw []any) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("tag is not a map: %T", item)
		}
		result[m["Key"].(string)] = m["Value"].(string)
	}
	return result
}

// UnmarshalDesiredState unmarshals a raw desired-state JSON into a map.
func UnmarshalDesiredState(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return doc
}

// AssertStringField checks a string field in the unmarshaled document.
func AssertStringField(t *testing.T, doc map[string]any, key, want string) {
	t.Helper()
	if got := doc[key]; got != want {
		t.Errorf("%s = %v, want %s", key, got, want)
	}
}

// AssertSGID checks that SecurityGroupIds contains exactly one entry matching want.
func AssertSGID(t *testing.T, doc map[string]any, want string) {
	t.Helper()
	sgIDs, ok := doc["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0] != want {
		t.Errorf("SecurityGroupIds = %v, want [%s]", doc["SecurityGroupIds"], want)
	}
}

// AssertIMDSv2 checks that MetadataOptions.HttpTokens is set to "required".
func AssertIMDSv2(t *testing.T, doc map[string]any) {
	t.Helper()
	meta, ok := doc["MetadataOptions"].(map[string]any)
	if !ok || meta["HttpTokens"] != "required" {
		t.Errorf("MetadataOptions.HttpTokens not 'required': %v", doc["MetadataOptions"])
	}
}

// AssertEBS checks the EBS block device mapping fields.
func AssertEBS(t *testing.T, doc map[string]any, wantSize int, wantDelete bool) {
	t.Helper()
	bdms, ok := doc["BlockDeviceMappings"].([]any)
	if !ok || len(bdms) != 1 {
		t.Fatalf("BlockDeviceMappings = %v, want 1 entry", doc["BlockDeviceMappings"])
	}
	bdm := bdms[0].(map[string]any)
	ebs := bdm["Ebs"].(map[string]any)
	if ebs["VolumeType"] != "gp3" {
		t.Errorf("EBS VolumeType = %v, want gp3", ebs["VolumeType"])
	}
	if ebs["VolumeSize"] != float64(wantSize) {
		t.Errorf("EBS VolumeSize = %v, want %d", ebs["VolumeSize"], wantSize)
	}
	if ebs["DeleteOnTermination"] != wantDelete {
		t.Errorf("EBS DeleteOnTermination = %v, want %v", ebs["DeleteOnTermination"], wantDelete)
	}
}

// AssertManagedByTag checks that the Tags array contains ManagedBy=fabrica.
func AssertManagedByTag(t *testing.T, raw json.RawMessage) {
	t.Helper()
	doc := UnmarshalDesiredState(t, raw)
	tags := ParseTags(t, doc["Tags"].([]any))
	if tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tags["ManagedBy"])
	}
}

// AssertIngressCidr checks that all ingress rules have the expected CIDR.
func AssertIngressCidr(t *testing.T, doc map[string]any, wantLen int, wantCidr string) {
	t.Helper()
	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok {
		t.Fatalf("SecurityGroupIngress is not an array")
	}
	if len(ingress) != wantLen {
		t.Fatalf("SecurityGroupIngress len = %d, want %d", len(ingress), wantLen)
	}
	for i, rule := range ingress {
		r := rule.(map[string]any)
		if r["CidrIp"] != wantCidr {
			t.Errorf("ingress[%d].CidrIp = %v, want %s", i, r["CidrIp"], wantCidr)
		}
	}
}
