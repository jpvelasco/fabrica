package lore

import (
	"strings"
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

func TestInstanceDesiredStateWithS3Store(t *testing.T) {
	plan := &CreatePlan{
		AmiID:               "ami-lore1",
		InstanceType:        "m5.xlarge",
		SubnetID:            "subnet-1",
		VolumeSize:          500,
		InstanceName:        "fabrica-lore",
		StoreBackend:        StoreBackendS3,
		InstanceProfileName: "fabrica-lore-profile",
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	// Verify IAM instance profile is set when S3 store is enabled.
	if doc["IamInstanceProfile"] != "fabrica-lore-profile" {
		t.Errorf("IamInstanceProfile = %v, want fabrica-lore-profile", doc["IamInstanceProfile"])
	}
}

func TestInstanceDesiredStateWithoutS3Store(t *testing.T) {
	plan := &CreatePlan{
		AmiID:        "ami-lore1",
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-1",
		VolumeSize:   500,
		InstanceName: "fabrica-lore",
		StoreBackend: StoreBackendLocal,
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	// Verify IAM instance profile is NOT set for local store.
	if _, hasProfile := doc["IamInstanceProfile"]; hasProfile {
		t.Error("IamInstanceProfile should not be set for local store backend")
	}
}

func TestBucketDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		StoreBucket:  "fabrica-lore-store-123-us-east-1",
		StoreBackend: StoreBackendS3,
	}
	raw, err := BucketDesiredState(plan)
	if err != nil {
		t.Fatalf("BucketDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["BucketName"] != "fabrica-lore-store-123-us-east-1" {
		t.Errorf("BucketName = %v", doc["BucketName"])
	}

	// Verify public access block.
	pab, ok := doc["PublicAccessBlockConfiguration"].(map[string]any)
	if !ok {
		t.Fatal("PublicAccessBlockConfiguration not found")
	}
	if pab["BlockPublicAcls"] != true {
		t.Error("BlockPublicAcls should be true")
	}
	if pab["BlockPublicPolicy"] != true {
		t.Error("BlockPublicPolicy should be true")
	}

	// Verify encryption.
	enc, ok := doc["BucketEncryption"].(map[string]any)
	if !ok {
		t.Fatal("BucketEncryption not found")
	}
	if enc == nil {
		t.Error("BucketEncryption should not be nil")
	}

	// Verify versioning.
	ver, ok := doc["VersioningConfiguration"].(map[string]any)
	if !ok {
		t.Fatal("VersioningConfiguration not found")
	}
	if ver["Status"] != "Enabled" {
		t.Errorf("Versioning Status = %v, want Enabled", ver["Status"])
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestRoleDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		RoleName:    "fabrica-lore-role",
		StoreBucket: "fabrica-lore-store-123-us-east-1",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["RoleName"] != "fabrica-lore-role" {
		t.Errorf("RoleName = %v", doc["RoleName"])
	}

	// Verify assume role policy allows EC2.
	arp, ok := doc["AssumeRolePolicyDocument"].(map[string]any)
	if !ok {
		t.Fatal("AssumeRolePolicyDocument not found")
	}
	stmts := arp["Statement"].([]any)
	if len(stmts) == 0 {
		t.Fatal("AssumeRolePolicyDocument has no statements")
	}
	stmt := stmts[0].(map[string]any)
	principal := stmt["Principal"].(map[string]any)
	if principal["Service"] != "ec2.amazonaws.com" {
		t.Errorf("Service principal = %v, want ec2.amazonaws.com", principal["Service"])
	}

	// Verify SSM managed policy is attached.
	arns, ok := doc["ManagedPolicyArns"].([]any)
	if !ok {
		t.Fatal("ManagedPolicyArns not found")
	}
	found := false
	for _, a := range arns {
		if a == "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore" {
			found = true
		}
	}
	if !found {
		t.Error("SSM managed policy not found in ManagedPolicyArns")
	}

	// Verify S3 + SSM output inline policies are present (DynamoDB policy is
	// absent because StoreTables is empty).
	policies, ok := doc["Policies"].([]any)
	if !ok {
		t.Fatal("Policies not found")
	}
	if len(policies) != 2 {
		t.Fatalf("Policies len = %d, want 2 (S3 + SSM output)", len(policies))
	}
	for _, p := range policies {
		pm := p.(map[string]any)
		if pm["PolicyName"] == "fabrica-lore-store-dynamodb" {
			t.Error("DynamoDB policy must be absent when StoreTables is empty")
		}
		if pm["PolicyName"] == "fabrica-ssm-output" {
			return
		}
	}
	t.Error("SSM output policy must be present in the Lore instance role")
}

func TestRoleDesiredStateOmitsDynamoDBWithoutTables(t *testing.T) {
	plan := &CreatePlan{
		RoleName:    "fabrica-lore-role",
		StoreBucket: "fabrica-lore-store-123456789012-us-east-1",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	policies := doc["Policies"].([]any)
	if len(policies) != 2 {
		t.Fatalf("Policies len = %d, want 2 (S3 + SSM output)", len(policies))
	}
	var sawDynamoDB, sawSSMOutput bool
	for _, p := range policies {
		pm := p.(map[string]any)
		switch pm["PolicyName"] {
		case "fabrica-lore-store-dynamodb":
			sawDynamoDB = true
		case "fabrica-ssm-output":
			sawSSMOutput = true
		}
	}
	if sawDynamoDB {
		t.Error("DynamoDB policy must be absent when StoreTables is empty")
	}
	if !sawSSMOutput {
		t.Error("SSM output policy must be present regardless of StoreTables")
	}
}

// TestRoleDesiredStateSSMOutput asserts the SSM output inline policy: this
// account's AmazonSSMManagedInstanceCore is a narrowed variant without
// ssm:PutParameter or logs:*, so the instance role needs an explicit
// least-privilege policy to publish SSM command output to the MDS parameter
// and the /fabrica/ssm/* CloudWatch Logs sink (the reliable retrieval path).
func TestRoleDesiredStateSSMOutput(t *testing.T) {
	plan := &CreatePlan{
		RoleName:    "fabrica-lore-role",
		StoreBucket: "fabrica-lore-store-123456789012-us-west-2",
		Region:      "us-west-2",
		Account:     "123456789012",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	var ssmPolicy map[string]any
	for _, p := range doc["Policies"].([]any) {
		pm := p.(map[string]any)
		if pm["PolicyName"] == "fabrica-ssm-output" {
			ssmPolicy = pm
		}
	}
	if ssmPolicy == nil {
		t.Fatal("fabrica-ssm-output policy not found")
	}
	pd := ssmPolicy["PolicyDocument"].(map[string]any)
	if pd["Version"] != "2012-10-17" {
		t.Errorf("PolicyDocument.Version = %v", pd["Version"])
	}
	stmts := pd["Statement"].([]any)
	if len(stmts) != 3 {
		t.Fatalf("Statement len = %d, want 3 (MDS param, log group, log stream)", len(stmts))
	}

	wantActions := []string{
		"ssm:PutParameter", "ssm:GetParameter", "ssm:DescribeParameters",
		"logs:CreateLogGroup",
		"logs:CreateLogStream", "logs:PutLogEvents",
	}
	seen := map[string]bool{}
	for _, s := range stmts {
		sm := s.(map[string]any)
		if sm["Effect"] != "Allow" {
			t.Errorf("statement %v has Effect %v, want Allow", sm["Sid"], sm["Effect"])
		}
		actions := sm["Action"].([]any)
		resources := sm["Resource"].([]any)
		for _, a := range actions {
			seen[a.(string)] = true
		}
		allowed := map[string]bool{
			"arn:aws:ssm:us-west-2:123456789012:parameter/MDS-*":             true,
			"arn:aws:logs:us-west-2:123456789012:log-group:/fabrica/ssm/*":   true,
			"arn:aws:logs:us-west-2:123456789012:log-group:/fabrica/ssm/*:*": true,
		}
		for _, r := range resources {
			rs, _ := r.(string)
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

func TestStoreTableDesiredStateFragments(t *testing.T) {
	plan := &CreatePlan{StoreBucket: "lore-store-123", StoreTables: []string{"lore-store-123-fragments"}}
	raw, err := StoreTableDesiredState(plan, "fragments")
	if err != nil {
		t.Fatalf("StoreTableDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["TableName"] != "lore-store-123-fragments" {
		t.Errorf("TableName = %v", doc["TableName"])
	}
	ks, ok := doc["KeySchema"].([]any)
	if !ok || len(ks) != 2 {
		t.Fatalf("KeySchema = %v, want 2 entries", doc["KeySchema"])
	}
	if ks[0].(map[string]any)["AttributeName"] != "hash" || ks[0].(map[string]any)["KeyType"] != "HASH" {
		t.Errorf("KeySchema[0] = %v", ks[0])
	}
	if ks[1].(map[string]any)["AttributeName"] != "repository_context" || ks[1].(map[string]any)["KeyType"] != "RANGE" {
		t.Errorf("KeySchema[1] = %v", ks[1])
	}
	if _, hasGSIs := doc["GlobalSecondaryIndexes"]; hasGSIs {
		t.Error("fragments table must not have GSIs")
	}
	attrs, ok := doc["AttributeDefinitions"].([]any)
	if !ok || len(attrs) != 2 {
		t.Fatalf("AttributeDefinitions = %v, want hash + repository_context", doc["AttributeDefinitions"])
	}
	if billing := doc["BillingMode"]; billing != "PAY_PER_REQUEST" {
		t.Errorf("BillingMode = %v, want PAY_PER_REQUEST (Cloud Control string)", billing)
	}
}

func TestStoreTableDesiredStateLocks(t *testing.T) {
	plan := &CreatePlan{StoreBucket: "lore-store-123", StoreTables: []string{"lore-store-123-locks"}}
	raw, err := StoreTableDesiredState(plan, "locks")
	if err != nil {
		t.Fatalf("StoreTableDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["TableName"] != "lore-store-123-locks" {
		t.Errorf("TableName = %v", doc["TableName"])
	}
	// KeySchema: hash (HASH) + repositoryBranch (RANGE).
	ks := doc["KeySchema"].([]any)
	if ks[0].(map[string]any)["AttributeName"] != "hash" || ks[1].(map[string]any)["AttributeName"] != "repositoryBranch" {
		t.Errorf("locks KeySchema = %v", ks)
	}
	// All attribute declarations needed by the key + GSI key types.
	wantAttrs := map[string]string{
		"hash": "B", "repositoryBranch": "B",
		"ownerId": "S", "repository": "B", "branch": "B", "description": "S",
	}
	attrs := doc["AttributeDefinitions"].([]any)
	if len(attrs) != len(wantAttrs) {
		t.Fatalf("AttributeDefinitions len = %d, want %d", len(attrs), len(wantAttrs))
	}
	seen := map[string]string{}
	for _, a := range attrs {
		m := a.(map[string]any)
		seen[m["AttributeName"].(string)] = m["AttributeType"].(string)
	}
	for name, typ := range wantAttrs {
		if seen[name] != typ {
			t.Errorf("attribute %s = %q, want %q", name, seen[name], typ)
		}
	}
	// Three GSIs, each projecting ALL.
	gsis, ok := doc["GlobalSecondaryIndexes"].([]any)
	if !ok || len(gsis) != 3 {
		t.Fatalf("GlobalSecondaryIndexes = %v, want 3", doc["GlobalSecondaryIndexes"])
	}
	gsiNames := map[string]map[string]any{}
	for _, g := range gsis {
		gm := g.(map[string]any)
		gsiNames[gm["IndexName"].(string)] = gm
		if gm["Projection"].(map[string]any)["ProjectionType"] != "ALL" {
			t.Errorf("GSI %s projection = %v, want ALL", gm["IndexName"], gm["Projection"])
		}
	}
	or := gsiNames["owner-repo-branch"]
	if or == nil {
		t.Fatal("GSI owner-repo-branch missing")
	}
	orKeys := or["KeySchema"].([]any)
	if orKeys[0].(map[string]any)["AttributeName"] != "ownerId" || orKeys[0].(map[string]any)["KeyType"] != "HASH" {
		t.Errorf("owner-repo-branch key = %v", orKeys)
	}
	if orKeys[1].(map[string]any)["AttributeName"] != "repositoryBranch" || orKeys[1].(map[string]any)["KeyType"] != "RANGE" {
		t.Errorf("owner-repo-branch range = %v", orKeys[1])
	}
	rb := gsiNames["repo-branch"]
	if rb == nil {
		t.Fatal("GSI repo-branch missing")
	}
	rbKeys := rb["KeySchema"].([]any)
	if rbKeys[0].(map[string]any)["AttributeName"] != "repository" || rbKeys[1].(map[string]any)["AttributeName"] != "branch" {
		t.Errorf("repo-branch keys = %v", rbKeys)
	}
	rbd := gsiNames["repo-branch-description"]
	if rbd == nil {
		t.Fatal("GSI repo-branch-description missing")
	}
	rbdKeys := rbd["KeySchema"].([]any)
	if rbdKeys[0].(map[string]any)["AttributeName"] != "repositoryBranch" || rbdKeys[1].(map[string]any)["AttributeName"] != "description" {
		t.Errorf("repo-branch-description keys = %v", rbdKeys)
	}
}

func TestStoreTableDesiredStateUnknownSuffix(t *testing.T) {
	plan := &CreatePlan{StoreBucket: "lore-store-123", StoreTables: []string{"lore-store-123-fragments"}}
	_, err := StoreTableDesiredState(plan, "nope")
	if err == nil {
		t.Fatal("expected error for unknown table suffix")
	}
}

func TestStoreTableDesiredStateManagedByTag(t *testing.T) {
	plan := &CreatePlan{StoreBucket: "lore-store-123", StoreTables: []string{"lore-store-123-locks"}}
	raw, err := StoreTableDesiredState(plan, "locks")
	if err != nil {
		t.Fatalf("StoreTableDesiredState: %v", err)
	}
	ec2state.AssertManagedByTag(t, raw)
}

func TestRoleDesiredStateStoreTables(t *testing.T) {
	plan := &CreatePlan{
		RoleName:    "fabrica-lore-role",
		StoreBucket: "fabrica-lore-store-123456789012-us-east-1",
		StoreTables: []string{
			"fabrica-lore-store-123456789012-us-east-1-fragments",
			"fabrica-lore-store-123456789012-us-east-1-metadata",
			"fabrica-lore-store-123456789012-us-east-1-mutable",
			"fabrica-lore-store-123456789012-us-east-1-locks",
		},
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	policies := doc["Policies"].([]any)
	if len(policies) != 3 {
		t.Fatalf("Policies len = %d, want 3 (S3 + DynamoDB + SSM output)", len(policies))
	}

	// Collect the S3 statement resources (must include ListBucketVersions + DeleteObjectVersion).
	var s3BucketActions, dynamoActions []string
	for _, p := range policies {
		pm := p.(map[string]any)
		pd := pm["PolicyDocument"].(map[string]any)
		stmts := pd["Statement"].([]any)
		for _, s := range stmts {
			sm := s.(map[string]any)
			actions, _ := sm["Action"].([]any)
			resources, _ := sm["Resource"].([]any)
			for _, r := range resources {
				rs, _ := r.(string)
				switch {
				case rs == "arn:aws:s3:::fabrica-lore-store-123456789012-us-east-1":
					for _, a := range actions {
						s3BucketActions = append(s3BucketActions, a.(string))
					}
				case strings.HasPrefix(rs, "arn:aws:dynamodb:"):
					for _, a := range actions {
						dynamoActions = append(dynamoActions, a.(string))
					}
				}
			}
		}
	}

	hasS3 := func(a string) bool {
		for _, x := range s3BucketActions {
			if x == a {
				return true
			}
		}
		return false
	}
	for _, a := range []string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketVersions"} {
		if !hasS3(a) {
			t.Errorf("S3 bucket actions missing %q (got %v)", a, s3BucketActions)
		}
	}
	hasDynamo := func(a string) bool {
		for _, x := range dynamoActions {
			if x == a {
				return true
			}
		}
		return false
	}
	for _, a := range []string{
		"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:Query",
		"dynamodb:BatchGetItem", "dynamodb:DescribeTable", "dynamodb:TransactWriteItems",
	} {
		if !hasDynamo(a) {
			t.Errorf("DynamoDB actions missing %q (got %v)", a, dynamoActions)
		}
	}

	// The locks GSI resource ARN must be present (plugin writes to indexes).
	var sawLocksGSI bool
	for _, p := range policies {
		pm := p.(map[string]any)
		pd := pm["PolicyDocument"].(map[string]any)
		stmts := pd["Statement"].([]any)
		for _, s := range stmts {
			sm := s.(map[string]any)
			resources, _ := sm["Resource"].([]any)
			for _, r := range resources {
				if rs, _ := r.(string); strings.HasSuffix(rs, "fabrica-lore-store-123456789012-us-east-1-locks/index/*") {
					sawLocksGSI = true
				}
			}
		}
	}
	if !sawLocksGSI {
		t.Error("locks GSI index/* ARN missing from role policy")
	}
}

func TestInstanceProfileDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		InstanceProfileName: "fabrica-lore-profile",
		RoleName:            "fabrica-lore-role",
	}
	raw, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatalf("InstanceProfileDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["InstanceProfileName"] != "fabrica-lore-profile" {
		t.Errorf("InstanceProfileName = %v", doc["InstanceProfileName"])
	}
	roles := doc["Roles"].([]any)
	if len(roles) != 1 || roles[0] != "fabrica-lore-role" {
		t.Errorf("Roles = %v, want [fabrica-lore-role]", roles)
	}
}
