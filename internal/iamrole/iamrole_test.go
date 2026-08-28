// Package iamrole provides shared IAM role desired-state helpers for plan layers.
package iamrole

import (
	"strings"
	"testing"
)

func TestAssumeRolePolicyDocument_Service(t *testing.T) {
	doc := AssumeRolePolicyDocument("ec2.amazonaws.com")
	if doc == nil {
		t.Fatal("AssumeRolePolicyDocument returned nil")
	}
	if v, ok := doc["Version"].(string); v != "2012-10-17" || !ok {
		t.Errorf("Version = %v, want 2012-10-17", v)
	}
	stmts, ok := doc["Statement"].([]map[string]any)
	if !ok || len(stmts) != 1 {
		t.Fatalf("Statement not a single-element array: %#v", doc["Statement"])
	}
	stmt := stmts[0]
	if stmt["Effect"] != "Allow" {
		t.Errorf("Effect = %v, want Allow", stmt["Effect"])
	}
	if stmt["Action"] != "sts:AssumeRole" {
		t.Errorf("Action = %v, want sts:AssumeRole", stmt["Action"])
	}
	principal, ok := stmt["Principal"].(map[string]any)
	if !ok {
		t.Fatalf("Principal not map: %#v", stmt["Principal"])
	}
	svc, ok := principal["Service"].(string)
	if !ok || svc != "ec2.amazonaws.com" {
		t.Errorf("Service = %v, want ec2.amazonaws.com", svc)
	}
}

func TestAssumeRolePolicyDocument_CodeBuild(t *testing.T) {
	doc := AssumeRolePolicyDocument("codebuild.amazonaws.com")
	stmts := doc["Statement"].([]map[string]any)
	principal := stmts[0]["Principal"].(map[string]any)
	if principal["Service"] != "codebuild.amazonaws.com" {
		t.Errorf("Service = %v, want codebuild.amazonaws.com", principal["Service"])
	}
}

func TestAssumeRolePolicyDocument_GameLift(t *testing.T) {
	doc := AssumeRolePolicyDocument("gamelift.amazonaws.com")
	stmts := doc["Statement"].([]map[string]any)
	principal := stmts[0]["Principal"].(map[string]any)
	if principal["Service"] != "gamelift.amazonaws.com" {
		t.Errorf("Service = %v, want gamelift.amazonaws.com", principal["Service"])
	}
}

func TestRoleTags_Basic(t *testing.T) {
	tags := RoleTags("my-role", nil)
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(tags))
	}
	if tags[0]["Key"] != "ManagedBy" || tags[0]["Value"] != "fabrica" {
		t.Errorf("tag[0] = %+v, want ManagedBy=fabrica", tags[0])
	}
	if tags[1]["Key"] != "Name" || tags[1]["Value"] != "my-role" {
		t.Errorf("tag[1] = %+v, want Name=my-role", tags[1])
	}
}

func TestRoleTags_WithExtra(t *testing.T) {
	extra := map[string]string{
		"FabricaModule": "ddc",
		"Environment":   "dev",
	}
	tags := RoleTags("ddc-role", extra)
	if len(tags) != 4 {
		t.Fatalf("got %d tags with 2 extra, want 4", len(tags))
	}
	// Build a map for easier lookup
	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag["Key"]] = tag["Value"]
	}
	if tagMap["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy = %q, want fabrica", tagMap["ManagedBy"])
	}
	if tagMap["Name"] != "ddc-role" {
		t.Errorf("Name = %q, want ddc-role", tagMap["Name"])
	}
	if tagMap["FabricaModule"] != "ddc" {
		t.Errorf("FabricaModule = %q, want ddc", tagMap["FabricaModule"])
	}
	if tagMap["Environment"] != "dev" {
		t.Errorf("Environment = %q, want dev", tagMap["Environment"])
	}
}

// TestSSMOutputPolicy_Shared asserts the shared SSM output policy: an explicit
// least-privilege policy that lets an instance publish SSM command output to
// the MDS parameter and the /fabrica/ssm/* CloudWatch Logs sink, because the
// account's AmazonSSMManagedInstanceCore is a narrowed variant without
// ssm:PutParameter or logs:*. Every SSM-using module role must attach this.
func TestSSMOutputPolicy_Shared(t *testing.T) {
	p := SSMOutputPolicy("us-west-2", "123456789012")
	if p["PolicyName"] != "fabrica-ssm-output" {
		t.Fatalf("PolicyName = %v, want fabrica-ssm-output", p["PolicyName"])
	}
	pd := p["PolicyDocument"].(map[string]any)
	if pd["Version"] != "2012-10-17" {
		t.Errorf("PolicyDocument.Version = %v, want 2012-10-17", pd["Version"])
	}
	stmts := pd["Statement"].([]map[string]any)
	if len(stmts) != 3 {
		t.Fatalf("Statement len = %d, want 3 (MDS param, log group, log stream)", len(stmts))
	}
	wantActions := []string{
		"ssm:PutParameter", "ssm:GetParameter", "ssm:DescribeParameters",
		"logs:CreateLogGroup",
		"logs:CreateLogStream", "logs:PutLogEvents",
	}
	seen := map[string]bool{}
	allowed := map[string]bool{
		"arn:aws:ssm:us-west-2:123456789012:parameter/MDS-*":             true,
		"arn:aws:logs:us-west-2:123456789012:log-group:/fabrica/ssm/*":   true,
		"arn:aws:logs:us-west-2:123456789012:log-group:/fabrica/ssm/*:*": true,
	}
	for _, sm := range stmts {
		if sm["Effect"] != "Allow" {
			t.Errorf("statement %v has Effect %v, want Allow", sm["Sid"], sm["Effect"])
		}
		for _, a := range sm["Action"].([]string) {
			seen[a] = true
		}
		for _, rs := range sm["Resource"].([]string) {
			if !allowed[rs] {
				t.Errorf("unexpected SSM output resource %q (must stay scoped to MDS-* and /fabrica/ssm/*)", rs)
			}
		}
	}
	for _, a := range wantActions {
		if !seen[a] {
			t.Errorf("SSM output policy missing action %q", a)
		}
	}
}

// TestSSMOutputPolicy_EmptyRegionAccount asserts the partition-agnostic
// fallback: callers that don't have region/account (tests, dry-runs) get
// "*" placeholders rather than malformed ARNs.
func TestSSMOutputPolicy_EmptyRegionAccount(t *testing.T) {
	p := SSMOutputPolicy("", "")
	stmts := p["PolicyDocument"].(map[string]any)["Statement"].([]map[string]any)
	if len(stmts) != 3 {
		t.Fatalf("Statement len = %d, want 3", len(stmts))
	}
	for _, sm := range stmts {
		for _, rs := range sm["Resource"].([]string) {
			if !strings.Contains(rs, ":parameter/MDS-*") &&
				!strings.Contains(rs, ":log-group:/fabrica/ssm/*") {
				t.Errorf("unexpected resource shape %q", rs)
			}
			// Both the region and account segments must be the wildcard.
			segments := strings.Split(strings.TrimPrefix(rs, "arn:aws:"), ":")
			if segments[1] != "*" || segments[2] != "*" {
				t.Errorf("resource %q does not use */* fallback for region/account", rs)
			}
		}
	}
}

func TestRoleTags_EmptyExtra(t *testing.T) {
	tags := RoleTags("simple-role", map[string]string{})
	if len(tags) != 2 {
		t.Errorf("got %d tags with empty extra, want 2", len(tags))
	}
}

func TestRoleTags_TagShape(t *testing.T) {
	tags := RoleTags("test", nil)
	for i, tag := range tags {
		if _, ok := tag["Key"]; !ok {
			t.Errorf("tag[%d] missing Key field", i)
		}
		if _, ok := tag["Value"]; !ok {
			t.Errorf("tag[%d] missing Value field", i)
		}
	}
}
