package export

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/state"
	"go.yaml.in/yaml/v3"
)

// TestReproHordeLogicalIDCollision reproduces defect #1: toLogicalID 12-char truncate
// maps fabrica-horde-role and fabrica-horde-agents-role to same key.
func TestReproHordeLogicalIDCollision(t *testing.T) {
	coord := toLogicalID("horde", "AWS::IAM::Role", "fabrica-horde-role")
	agent := toLogicalID("horde", "AWS::IAM::Role", "fabrica-horde-agents-role")
	if coord == agent {
		t.Fatalf("COLLISION: coordinator %q == agent %q", coord, agent)
	}
	// Also test fabrica-horde-agent-role variant mentioned in task
	agent2 := toLogicalID("horde", "AWS::IAM::Role", "fabrica-horde-agent-role")
	if coord == agent2 {
		t.Fatalf("COLLISION: coordinator %q == agent2 %q", coord, agent2)
	}
}

// TestReproHordeExportBothRoles verifies export of coordinator + agents yields two distinct roles.
func TestReproHordeExportBothRoles(t *testing.T) {
	st := state.NewState(exportAccount, exportRegion)
	// Horde module contains both coordinator and agent roles as recorded state would.
	st.UpsertModule("horde", "ami-coord", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-role", Properties: map[string]string{"RoleName": "fabrica-horde-role"}},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-agents-role", Properties: map[string]string{"role": "agent", "RoleName": "fabrica-horde-agents-role"}},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord", Properties: map[string]string{"instanceType": "m7i.2xlarge", "volumeSize": "100"}},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord", Properties: map[string]string{}},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-role", Properties: map[string]string{}}, // duplicate to ensure handling? Actually we have two roles already
	})
	// Use distinct identifiers: we already added both roles, but need to avoid duplicate identical identifier; remove duplicate
	// Rebuild state with correct resources (2 roles)
	st = state.NewState(exportAccount, exportRegion)
	st.UpsertModule("horde", "ami-coord", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord", Properties: map[string]string{"GroupName": "fabrica-horde-sg"}},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord", Properties: map[string]string{"instanceType": "m7i.2xlarge", "volumeSize": "100"}},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-role", Properties: map[string]string{"RoleName": "fabrica-horde-role"}},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-agents-role", Properties: map[string]string{"RoleName": "fabrica-horde-agents-role", "role": "agent"}},
		{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-horde-profile", Properties: map[string]string{"InstanceProfileName": "fabrica-horde-profile"}},
		{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-horde-agents-profile", Properties: map[string]string{"InstanceProfileName": "fabrica-horde-agents-profile", "role": "agent"}},
	})
	cfg := testConfigWithHorde()
	// CFN
	cfData, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("CFN Generate: %v", err)
	}
	var tmpl map[string]any
	if err := yaml.Unmarshal(cfData, &tmpl); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	resources, ok := tmpl["Resources"].(map[string]any)
	if !ok {
		t.Fatal("missing Resources")
	}
	// Count IAM roles in CFN output
	roleCount := 0
	roleLogicalIDs := map[string]bool{}
	for lid, res := range resources {
		rm, _ := res.(map[string]any)
		if rm["Type"] == "AWS::IAM::Role" {
			roleCount++
			roleLogicalIDs[lid] = true
			t.Logf("CFN role logicalID: %s", lid)
		}
	}
	if roleCount != 2 {
		t.Fatalf("expected 2 IAM roles in CFN export, got %d (%v) — collision likely overwrote one", roleCount, roleLogicalIDs)
	}
	if len(roleLogicalIDs) != 2 {
		t.Fatalf("logical IDs not distinct: %v", roleLogicalIDs)
	}
	// TF
	tfData, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("TF Generate: %v", err)
	}
	tfStr := string(tfData)
	// Both role names must appear, and logicalID-derived TF resource names must be distinct
	coordLID := toLogicalID("horde", "AWS::IAM::Role", "fabrica-horde-role")
	agentLID := toLogicalID("horde", "AWS::IAM::Role", "fabrica-horde-agents-role")
	if coordLID == agentLID {
		t.Fatalf("toLogicalID collision still present: %q", coordLID)
	}
	gen := &terraformGenerator{}
	coordTF := gen.tfResourceName(coordLID)
	agentTF := gen.tfResourceName(agentLID)
	if coordTF == agentTF {
		t.Fatalf("TF resource names collide: %q", coordTF)
	}
	if !strings.Contains(tfStr, coordTF) {
		t.Errorf("TF missing coordinator role %q (logical %q)", coordTF, coordLID)
	}
	if !strings.Contains(tfStr, agentTF) {
		t.Errorf("TF missing agent role %q (logical %q)", agentTF, agentLID)
	}
	// Ensure both role identifiers present in TF output
	if !strings.Contains(tfStr, "fabrica-horde-role") || !strings.Contains(tfStr, "fabrica-horde-agents-role") {
		t.Errorf("TF output missing one of the role identifiers:\n%s", tfStr)
	}
}

// TestReproCIInlinePolicyExport verifies CI export includes fabrica-ci-inline via helper.
func TestReproCIInlinePolicyExport(t *testing.T) {
	st := state.NewState(exportAccount, exportRegion)
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild", Properties: map[string]string{"RoleName": "fabrica-ci-codebuild"}},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci", Properties: map[string]string{"Name": "fabrica-ci"}},
	})
	cfg := testConfigWithCI()
	// Build export module and check Policies
	ms := state.ModuleState{Name: "ci", Status: "ready", Resources: st.Modules[0].Resources}
	mod := buildModule(ms, cfg, exportAccount, exportRegion)
	var got []map[string]any
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::IAM::Role" {
			if p, ok := r.Properties["Policies"].([]map[string]any); ok {
				got = p
			}
		}
	}
	if len(got) == 0 {
		t.Fatal("ci role export missing inline Policies — expected fabrica-ci-inline")
	}
	found := false
	for _, p := range got {
		if p["PolicyName"] == "fabrica-ci-inline" {
			found = true
			doc, _ := p["PolicyDocument"].(map[string]any)
			j, _ := json.Marshal(doc)
			s := string(j)
			for _, want := range []string{"logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents", "ec2:DescribeInstances", "ec2:CreateNetworkInterface"} {
				if !strings.Contains(s, want) {
					t.Errorf("ci inline policy missing %q", want)
				}
			}
		}
	}
	if !found {
		t.Fatalf("fabrica-ci-inline not found in ci export policies: %v", got)
	}
}

// TestReproDeployInlinePolicyExport verifies deploy export includes fabrica-deploy-s3-read
func TestReproDeployInlinePolicyExport(t *testing.T) {
	st := state.NewState(exportAccount, exportRegion)
	st.UpsertModule("deploy", "v1", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift", Properties: map[string]string{"RoleName": "fabrica-deploy-gamelift"}},
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1", Properties: map[string]string{"Name": "fabrica-deploy"}},
	})
	cfg := testConfigWithDeploy()
	cfg.Deploy.BuildBucket = "my-build-bucket"
	ms := state.ModuleState{Name: "deploy", Status: "ready", Resources: st.Modules[0].Resources}
	mod := buildModule(ms, cfg, exportAccount, exportRegion)
	var got []map[string]any
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::IAM::Role" {
			if p, ok := r.Properties["Policies"].([]map[string]any); ok {
				got = p
			}
		}
	}
	if len(got) == 0 {
		t.Fatal("deploy role export missing inline Policies — expected fabrica-deploy-s3-read")
	}
	found := false
	for _, p := range got {
		if p["PolicyName"] == "fabrica-deploy-s3-read" {
			found = true
			doc, _ := p["PolicyDocument"].(map[string]any)
			j, _ := json.Marshal(doc)
			s := string(j)
			if !strings.Contains(s, "s3:GetObject") {
				t.Errorf("deploy inline missing s3:GetObject")
			}
			if !strings.Contains(s, "arn:aws:s3:::my-build-bucket/*") {
				t.Errorf("deploy inline missing bucket ARN, got %s", s)
			}
			// Check helper equality with plan layer
			plan := deploy.NewSetupPlan(cfg.Deploy, exportAccount, exportRegion)
			raw, _ := deploy.RoleDesiredState(plan)
			var doc2 map[string]any
			_ = json.Unmarshal(raw, &doc2)
			if pols, ok := doc2["Policies"].([]any); ok && len(pols) > 0 {
				if pm, ok := pols[0].(map[string]any); ok {
					wantDoc, _ := json.Marshal(pm["PolicyDocument"])
					gotDoc, _ := json.Marshal(doc)
					if string(wantDoc) != string(gotDoc) {
						t.Errorf("deploy export doc != create doc:\ngot  %s\nwant %s", gotDoc, wantDoc)
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("fabrica-deploy-s3-read not found: %v", got)
	}
}

// TestReproCIHelperEquality verifies create desired-state and export use same helper (byte-equal)
func TestReproCIHelperEquality(t *testing.T) {
	// This test will require iamrole.CI policy helper to exist.
	// If helper not yet implemented, this will fail to compile or will report mismatch.
	cfg := config.Defaults()
	cfg.CI.ProjectName = "fabrica-ci"
	plan, err := ci.NewCreatePlan(context.TODO(), cfg.CI, exportAccount, exportRegion, "", nil)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	raw, err := ci.RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var createDoc map[string]any
	if err := json.Unmarshal(raw, &createDoc); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	createPols, _ := createDoc["Policies"].([]any)
	if len(createPols) == 0 {
		t.Fatal("create has no Policies")
	}
	createPol, _ := createPols[0].(map[string]any)
	createPD, _ := json.Marshal(createPol["PolicyDocument"])

	// Build export's expected via inlinePoliciesForRole (which should use same helper)
	policies := inlinePoliciesForRole("ci", cfg, exportAccount, exportRegion)
	if len(policies) == 0 {
		t.Fatal("export inlinePoliciesForRole returned nil for ci")
	}
	exportPD, _ := json.Marshal(policies[0]["PolicyDocument"])
	if string(createPD) != string(exportPD) {
		t.Fatalf("CI helper mismatch create vs export:\ncreate %s\nexport %s", createPD, exportPD)
	}
}

func TestReproDeployHelperEquality(t *testing.T) {
	cfg := config.Defaults()
	cfg.Deploy.BuildBucket = "my-build-bucket"
	plan := deploy.NewSetupPlan(cfg.Deploy, exportAccount, exportRegion)
	raw, err := deploy.RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var createDoc map[string]any
	_ = json.Unmarshal(raw, &createDoc)
	createPols, _ := createDoc["Policies"].([]any)
	createPol, _ := createPols[0].(map[string]any)
	createPD, _ := json.Marshal(createPol["PolicyDocument"])

	policies := inlinePoliciesForRole("deploy", cfg, exportAccount, exportRegion)
	if len(policies) == 0 {
		t.Fatal("export inlinePoliciesForRole returned nil for deploy")
	}
	exportPD, _ := json.Marshal(policies[0]["PolicyDocument"])
	if string(createPD) != string(exportPD) {
		t.Fatalf("Deploy helper mismatch:\ncreate %s\nexport %s", createPD, exportPD)
	}
}
