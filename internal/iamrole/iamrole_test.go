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

func TestCICodeBuildInlinePolicy_Shared(t *testing.T) {
	p := CICodeBuildInlinePolicy("us-west-2", "123456789012", "fabrica-ci")
	if p["PolicyName"] != "fabrica-ci-inline" {
		t.Fatalf("PolicyName = %v, want fabrica-ci-inline", p["PolicyName"])
	}
	pd := p["PolicyDocument"].(map[string]any)
	if pd["Version"] != "2012-10-17" {
		t.Errorf("Version = %v, want 2012-10-17", pd["Version"])
	}
	stmts := pd["Statement"].([]map[string]any)
	if len(stmts) != 2 {
		t.Fatalf("Statement len = %d, want 2 (logs + ec2)", len(stmts))
	}
	// Logs statement
	logsStmt := stmts[0]
	if logsStmt["Effect"] != "Allow" {
		t.Errorf("logs Effect = %v, want Allow", logsStmt["Effect"])
	}
	if got, want := logsStmt["Action"], []string{"logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"}; len(got.([]string)) != len(want) {
		t.Errorf("logs Action len = %d, want %d", len(got.([]string)), len(want))
	}
	resources := logsStmt["Resource"].([]string)
	if len(resources) != 2 {
		t.Fatalf("logs Resource len = %d, want 2", len(resources))
	}
	if !strings.Contains(resources[0], "arn:aws:logs:us-west-2:123456789012:log-group:/aws/codebuild/fabrica-ci*") {
		t.Errorf("logs resource 0 = %q", resources[0])
	}
	if !strings.Contains(resources[1], "arn:aws:logs:us-west-2:123456789012:log-group:/aws/codebuild/fabrica-ci*:*") {
		t.Errorf("logs resource 1 = %q", resources[1])
	}
	// EC2 statement
	ec2Stmt := stmts[1]
	if ec2Stmt["Resource"] != "*" {
		t.Errorf("ec2 Resource = %v, want *", ec2Stmt["Resource"])
	}
	actions := ec2Stmt["Action"].([]string)
	wantEC2 := map[string]bool{
		"ec2:CreateNetworkInterface":           true,
		"ec2:CreateNetworkInterfacePermission": true,
		"ec2:DeleteNetworkInterface":           true,
		"ec2:DescribeDhcpOptions":              true,
		"ec2:DescribeInstances":                true,
		"ec2:DescribeNetworkInterfaces":        true,
		"ec2:DescribeSecurityGroups":           true,
		"ec2:DescribeSubnets":                  true,
		"ec2:DescribeTags":                     true,
		"ec2:DescribeVpcs":                     true,
		"ec2:CreateTags":                       true,
		"ec2:DeleteTags":                       true,
	}
	for _, a := range actions {
		if !wantEC2[a] {
			t.Errorf("unexpected ec2 action %q", a)
		}
		delete(wantEC2, a)
	}
	for missing := range wantEC2 {
		t.Errorf("missing ec2 action %q", missing)
	}
}

func TestCICodeBuildInlinePolicy_EmptyRegionAccount(t *testing.T) {
	p := CICodeBuildInlinePolicy("", "", "fabrica-ci")
	stmts := p["PolicyDocument"].(map[string]any)["Statement"].([]map[string]any)
	logsRes := stmts[0]["Resource"].([]string)[0]
	if !strings.Contains(logsRes, "arn:aws:logs:*:*:log-group:/aws/codebuild/fabrica-ci*") {
		t.Errorf("empty fallback resource = %q, want */*", logsRes)
	}
}

func TestDeployS3ReadPolicy_Shared(t *testing.T) {
	p := DeployS3ReadPolicy("my-bucket")
	if p["PolicyName"] != "fabrica-deploy-s3-read" {
		t.Fatalf("PolicyName = %v, want fabrica-deploy-s3-read", p["PolicyName"])
	}
	pd := p["PolicyDocument"].(map[string]any)
	stmts := pd["Statement"].([]map[string]any)
	if len(stmts) != 1 {
		t.Fatalf("Statement len = %d, want 1", len(stmts))
	}
	stmt := stmts[0]
	if stmt["Effect"] != "Allow" {
		t.Errorf("Effect = %v, want Allow", stmt["Effect"])
	}
	actions := stmt["Action"].([]string)
	if len(actions) != 1 || actions[0] != "s3:GetObject" {
		t.Errorf("Action = %v, want [s3:GetObject]", actions)
	}
	if stmt["Resource"] != "arn:aws:s3:::my-bucket/*" {
		t.Errorf("Resource = %v, want arn:aws:s3:::my-bucket/*", stmt["Resource"])
	}
}

func TestRoleDocument_Basic(t *testing.T) {
	raw, err := RoleDocument("my-role", ServiceEC2, []string{"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"}, []map[string]any{SSMOutputPolicy("us-east-1", "123")}, nil)
	if err != nil {
		t.Fatalf("RoleDocument: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("RoleDocument returned empty")
	}
	// Ensure it round-trips to valid JSON with expected keys
	if !strings.Contains(string(raw), "my-role") {
		t.Errorf("RoleDocument missing role name")
	}
	if !strings.Contains(string(raw), "ec2.amazonaws.com") {
		t.Errorf("RoleDocument missing trust service")
	}
}

func TestRoleDocument_NilPolicies(t *testing.T) {
	raw, err := RoleDocument("my-role", ServiceEC2, nil, nil, map[string]string{"Env": "test"})
	if err != nil {
		t.Fatalf("RoleDocument: %v", err)
	}
	if strings.Contains(string(raw), "Policies") {
		t.Errorf("RoleDocument with nil policies should not contain Policies")
	}
	if !strings.Contains(string(raw), "Env") {
		t.Errorf("RoleDocument missing extra tag")
	}
}
