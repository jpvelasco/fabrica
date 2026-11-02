package drift

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/state"
)

// --- PlanRemediation tests ---

func TestPlanRemediation_MissingEC2Instance(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123", Status: InSync},
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: Missing, Details: "resource not found in live state"},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	cfg.Horde.InstanceType = "m7i.2xlarge"
	cfg.Horde.AmiID = "ami-fake"

	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 1 {
		t.Errorf("expected 1 to fix, got %d", plan.ToFix)
	}
	if plan.ToSkip != 0 {
		t.Errorf("expected 0 to skip, got %d", plan.ToSkip)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action (only the missing instance), got %d", len(plan.Actions))
	}

	// Only the missing instance should be in the plan (InSync resources
	// are filtered out).
	createActions := 0
	for _, a := range plan.Actions {
		if a.Kind == ActionCreate {
			createActions++
			if a.TypeName != cloud.TypeAWSEC2Instance {
				t.Errorf("expected create for EC2 instance, got %s", a.TypeName)
			}
			if a.Resource == nil {
				t.Error("expected non-nil resource for create action")
			} else {
				// Verify desired state contains expected fields.
				var ds map[string]any
				if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
					t.Fatalf("invalid desired state: %v", err)
				}
				if ds["InstanceType"] != "m7i.2xlarge" {
					t.Errorf("expected InstanceType m7i.2xlarge, got %v", ds["InstanceType"])
				}
				if ds["ImageId"] != "ami-fake" {
					t.Errorf("expected ImageId ami-fake, got %v", ds["ImageId"])
				}
			}
		}
	}
	if createActions != 1 {
		t.Errorf("expected 1 create action, got %d", createActions)
	}
}

func TestPlanRemediation_MissingSecurityGroup(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "perforce",
			Resources: []DriftResult{
				{Module: "perforce", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123", Status: Missing},
				{Module: "perforce", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: InSync},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 1 {
		t.Errorf("expected 1 to fix, got %d", plan.ToFix)
	}

	// Verify the SG has a desired state with description.
	for _, a := range plan.Actions {
		if a.Kind == ActionCreate && a.TypeName == cloud.TypeAWSEC2SecurityGroup {
			if a.Resource == nil {
				t.Fatal("expected non-nil resource for SG create")
				return
			}
			var ds map[string]any
			if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
				t.Fatalf("invalid desired state: %v", err)
			}
			if ds["GroupDescription"] == "" {
				t.Error("expected GroupDescription in SG desired state")
			}
		}
	}
}

func TestPlanRemediation_MissingIAMRole(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSIAMRole, Identifier: "fabrica-ci-role", Properties: map[string]string{
			"service": "ec2.amazonaws.com",
		}},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "ci",
			Resources: []DriftResult{
				{Module: "ci", TypeName: cloud.TypeAWSIAMRole, Identifier: "fabrica-ci-role", Status: Missing},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	// IAM roles are not supported for auto-remediation (canRecreate returns false).
	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix for IAM role, got %d", plan.ToFix)
	}
	if plan.ToSkip != 1 {
		t.Errorf("expected 1 to skip, got %d", plan.ToSkip)
	}
	for _, a := range plan.Actions {
		if a.Kind != ActionSkip {
			t.Errorf("expected skip for IAM role, got %s", a.Kind)
		}
	}
}

func TestPlanRemediation_MismatchSkipped(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-expected", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: Mismatch, Details: "attribute mismatch: InstanceType: recorded=m7i.2xlarge, live=m5.xlarge"},
			},
		}},
		Mismatch: 1,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix for mismatch, got %d", plan.ToFix)
	}
	if plan.ToSkip != 1 {
		t.Errorf("expected 1 to skip, got %d", plan.ToSkip)
	}
	if plan.ReportOnly != 1 {
		t.Errorf("expected 1 report-only, got %d", plan.ReportOnly)
	}

	for _, a := range plan.Actions {
		if a.Kind != ActionSkip {
			t.Errorf("expected skip for mismatch, got %s", a.Kind)
		}
		if a.Resource != nil {
			t.Error("expected nil resource for skip action")
		}
	}
}

func TestPlanRemediation_ExtraSkipped(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: InSync},
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-extra", Status: Extra, Details: "resource exists in live state but not in recorded state"},
			},
		}},
		Extra: 1,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix for extra, got %d", plan.ToFix)
	}
	if plan.ToSkip != 1 {
		t.Errorf("expected 1 to skip for extra, got %d", plan.ToSkip)
	}
	if plan.ReportOnly != 1 {
		t.Errorf("expected 1 report-only for extra, got %d", plan.ReportOnly)
	}

	for _, a := range plan.Actions {
		if a.Drift == Extra && a.Kind != ActionSkip {
			t.Errorf("expected skip for extra, got %s", a.Kind)
		}
	}
}

func TestPlanRemediation_ErrorSkipped(t *testing.T) {
	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: Error, Details: "no resource client available"},
			},
		}},
		Errors: 1,
	}

	st := state.NewState("123456789012", "us-east-1")
	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix for error, got %d", plan.ToFix)
	}
	if plan.ToSkip != 1 {
		t.Errorf("expected 1 to skip for error, got %d", plan.ToSkip)
	}
}

func TestPlanRemediation_AllInSync(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123", Status: InSync},
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: InSync},
			},
		}},
		InSync: 2,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	// InSync resources should not produce any actions.
	if len(plan.Actions) != 0 {
		t.Errorf("expected 0 actions for all inSync, got %d", len(plan.Actions))
	}
	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix, got %d", plan.ToFix)
	}
}

func TestPlanRemediation_EmptyReport(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	report := &DriftReport{}
	cfg := config.Defaults()

	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 0 || plan.ToSkip != 0 || len(plan.Actions) != 0 {
		t.Errorf("expected empty plan for empty report, got ToFix=%d ToSkip=%d Actions=%d", plan.ToFix, plan.ToSkip, len(plan.Actions))
	}
}

func TestPlanRemediation_MissingCodeBuild(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "ci",
			Resources: []DriftResult{
				{Module: "ci", TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci", Status: Missing, Details: "CodeBuild project not found"},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)

	// CodeBuild is Missing but unsupported for auto-remediation — it should
	// be ActionSkip with a reason, not ActionCreate.
	if plan.ToFix != 0 {
		t.Errorf("expected 0 to fix for unsupported type, got %d", plan.ToFix)
	}
	if plan.ToSkip != 1 {
		t.Errorf("expected 1 to skip, got %d", plan.ToSkip)
	}
	if plan.ReportOnly != 1 {
		t.Errorf("expected 1 report-only, got %d", plan.ReportOnly)
	}
	for _, a := range plan.Actions {
		if a.Kind != ActionSkip {
			t.Errorf("expected skip for CodeBuild, got %s", a.Kind)
		}
		if !strings.Contains(a.Reason, "unsupported") {
			t.Errorf("expected 'unsupported' in reason, got: %s", a.Reason)
		}
	}
}

// --- ApplyRemediation tests ---

func TestApplyRemediation_Success(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	})

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-123",
				Drift:      Missing,
				Kind:       ActionCreate,
				Resource: &cloud.Resource{
					TypeName:     cloud.TypeAWSEC2Instance,
					Identifier:   "i-123",
					DesiredState: json.RawMessage(`{"InstanceType":"m7i.2xlarge"}`),
				},
			},
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-extra",
				Drift:      Extra,
				Kind:       ActionSkip,
				Reason:     "extra resources are report-only in V1",
			},
		},
		ToFix: 1,
	}

	var created []*cloud.Resource
	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, a *RemediationAction) error {
		created = append(created, a.Resource)
		return nil
	})

	if len(result.Applied) != 1 {
		t.Errorf("expected 1 applied, got %d", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(result.Skipped))
	}
	if len(result.Failed) != 0 {
		t.Errorf("expected 0 failed, got %d", len(result.Failed))
	}
	if len(created) != 1 {
		t.Errorf("expected 1 resource created, got %d", len(created))
	}
}

func TestApplyRemediation_PartialFailure(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	})

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2SecurityGroup,
				Identifier: "sg-123",
				Drift:      Missing,
				Kind:       ActionCreate,
				Resource: &cloud.Resource{
					TypeName:     cloud.TypeAWSEC2SecurityGroup,
					Identifier:   "sg-123",
					DesiredState: json.RawMessage(`{"GroupDescription":"test"}`),
				},
			},
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-123",
				Drift:      Missing,
				Kind:       ActionCreate,
				Resource: &cloud.Resource{
					TypeName:     cloud.TypeAWSEC2Instance,
					Identifier:   "i-123",
					DesiredState: json.RawMessage(`{"InstanceType":"m7i.2xlarge"}`),
				},
			},
		},
		ToFix: 2,
	}

	callCount := 0
	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, a *RemediationAction) error {
		callCount++
		if a.TypeName == cloud.TypeAWSEC2Instance {
			return errors.New("simulated create failure")
		}
		return nil
	})

	// SG should be applied; instance should fail; remaining should be skipped.
	if len(result.Applied) != 1 {
		t.Errorf("expected 1 applied (SG), got %d", len(result.Applied))
	}
	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failed (instance), got %d", len(result.Failed))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if callCount != 2 {
		t.Errorf("expected 2 create calls, got %d", callCount)
	}
}

func TestApplyRemediation_NoCreateSeam(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{})

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-123",
				Drift:      Missing,
				Kind:       ActionCreate,
				Resource: &cloud.Resource{
					TypeName:     cloud.TypeAWSEC2Instance,
					Identifier:   "i-123",
					DesiredState: json.RawMessage(`{}`),
				},
			},
		},
	}

	// ApplyRemediation with nil create seam — but we pass nil here.
	result := ApplyRemediation(context.Background(), plan, st, nil)

	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failed when create seam is nil, got %d", len(result.Failed))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestApplyRemediation_NoDesiredState(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{})

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{
				Module:     "ci",
				TypeName:   "AWS::CodeBuild::Project",
				Identifier: "fabrica-ci",
				Drift:      Missing,
				Kind:       ActionCreate,
				Resource: &cloud.Resource{
					TypeName:   "AWS::CodeBuild::Project",
					Identifier: "fabrica-ci",
					// No DesiredState — unsupported type.
				},
			},
		},
	}

	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, _ *RemediationAction) error {
		t.Fatal("create should not be called for resource without desired state")
		return nil
	})

	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failed for no desired state, got %d", len(result.Failed))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
}

func TestApplyRemediation_NothingToFix(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{
				Module:     "horde",
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-123",
				Drift:      Extra,
				Kind:       ActionSkip,
				Reason:     "report-only",
			},
		},
		ToSkip: 1,
	}

	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, _ *RemediationAction) error {
		t.Fatal("create should not be called")
		return nil
	})

	if len(result.Applied) != 0 {
		t.Errorf("expected 0 applied, got %d", len(result.Applied))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(result.Skipped))
	}
}

// --- rebuildSGDesiredState per-module tests ---

// rebuildSGDoc rebuilds an SG desired state and unmarshals it, failing the
// test on a rebuild error or malformed JSON.
func rebuildSGDoc(t *testing.T, rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) map[string]any {
	t.Helper()
	ds, err := rebuildSGDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuildSGDesiredState: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	return m
}

func sgIngressRules(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, ok := m["SecurityGroupIngress"].([]any)
	if !ok {
		t.Fatalf("SecurityGroupIngress missing or not an array: %v", m["SecurityGroupIngress"])
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func TestRebuildSGDesiredState_Horde(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-123"
	cfg.Horde.AllowedCIDR = "10.0.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "horde"}, cfg)
	if m["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("expected canonical group name, got %v", m["GroupName"])
	}
	if m["VpcId"] != "vpc-123" {
		t.Errorf("expected VpcId vpc-123, got %v", m["VpcId"])
	}
	rules := sgIngressRules(t, m)
	if len(rules) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(rules))
	}
	if rules[0]["FromPort"].(float64) != 5000 || rules[1]["FromPort"].(float64) != 5002 {
		t.Errorf("expected default ports 5000/5002, got %v/%v", rules[0]["FromPort"], rules[1]["FromPort"])
	}
	if rules[0]["CidrIp"] != "10.0.0.0/16" || rules[1]["CidrIp"] != "10.0.0.0/16" {
		t.Errorf("config CIDR not applied to both rules: %v", rules)
	}
}

func TestRebuildSGDesiredState_HordeNonDefaultPorts(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-123"
	cfg.Horde.Port = 5100
	cfg.Horde.GRPCPort = 5102

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "horde"}, cfg)
	rules := sgIngressRules(t, m)
	if rules[0]["FromPort"].(float64) != 5100 || rules[1]["FromPort"].(float64) != 5102 {
		t.Errorf("expected configured ports 5100/5102, got %v/%v", rules[0]["FromPort"], rules[1]["FromPort"])
	}
}

func TestRebuildSGDesiredState_HordeAgentSG(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-coord"
	cfg.Horde.Agents.VPCID = "vpc-agents"

	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{"role": "agent"},
	}
	m := rebuildSGDoc(t, rec, &state.ModuleState{Name: "horde"}, cfg)
	if m["GroupName"] != "fabrica-horde-agents-sg" {
		t.Errorf("expected agent group name, got %v", m["GroupName"])
	}
	if m["VpcId"] != "vpc-agents" {
		t.Errorf("expected agents VPC placement, got %v", m["VpcId"])
	}
	if _, has := m["SecurityGroupIngress"]; has {
		t.Errorf("agent SG must have no inbound rules, got %v", m["SecurityGroupIngress"])
	}
}

// Without an explicit agents VPC, the agent group follows the coordinator's
// VPC placement.
func TestRebuildSGDesiredState_HordeAgentSG_CoordinatorVPCFallback(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-coord"

	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{"role": "agent"},
	}
	m := rebuildSGDoc(t, rec, &state.ModuleState{Name: "horde"}, cfg)
	if m["VpcId"] != "vpc-coord" {
		t.Errorf("expected coordinator VPC fallback, got %v", m["VpcId"])
	}
}

func TestRebuildSGDesiredState_Lore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.VPCId = "vpc-lore"
	cfg.Lore.AllowedCIDR = "10.0.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "lore"}, cfg)
	rules := sgIngressRules(t, m)
	if len(rules) != 3 {
		t.Fatalf("expected 3 ingress rules (gRPC tcp, QUIC udp, health tcp), got %d", len(rules))
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
		if rules[i]["IpProtocol"] != w.proto || rules[i]["FromPort"].(float64) != w.port {
			t.Errorf("rule %d = %v, want %s/%v", i, rules[i], w.proto, w.port)
		}
	}
}

func TestRebuildSGDesiredState_DDC(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.VPCId = "vpc-ddc"
	cfg.DDC.AllowedCIDR = "10.0.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "ddc"}, cfg)
	if m["GroupName"] != "fabrica-ddc-sg" {
		t.Errorf("expected home group name, got %v", m["GroupName"])
	}
	rules := sgIngressRules(t, m)
	if len(rules) != 2 {
		t.Fatalf("expected 2 ingress rules (80, 8080), got %d", len(rules))
	}
}

func TestRebuildSGDesiredState_DDCConfiguredPortsAndCIDRs(t *testing.T) {
	// Issue #531 repro: non-default DDC ports and distinct public/internal
	// CIDRs must flow into the rebuilt group.
	cfg := config.Defaults()
	cfg.DDC.VPCId = "vpc-ddc"
	cfg.DDC.PublicPort = 8088
	cfg.DDC.InternalPort = 9099
	cfg.DDC.AllowedCIDR = "1.2.3.4/32"
	cfg.DDC.InternalCIDR = "10.99.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "ddc"}, cfg)
	rules := sgIngressRules(t, m)
	if len(rules) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(rules))
	}
	if rules[0]["FromPort"].(float64) != 8088 || rules[0]["CidrIp"] != "1.2.3.4/32" {
		t.Errorf("public rule = %v, want 8088 on public CIDR", rules[0])
	}
	if rules[1]["FromPort"].(float64) != 9099 || rules[1]["CidrIp"] != "10.99.0.0/16" {
		t.Errorf("internal rule = %v, want 9099 on internal CIDR", rules[1])
	}
}

func TestRebuildSGDesiredState_DDCScylla(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.Backend = "scylla"
	cfg.DDC.InternalCIDR = "10.99.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "ddc"}, cfg)
	rules := sgIngressRules(t, m)
	if len(rules) != 3 {
		t.Fatalf("expected 3 ingress rules with scylla backend, got %d", len(rules))
	}
	if rules[2]["FromPort"].(float64) != 9042 || rules[2]["CidrIp"] != "10.99.0.0/16" {
		t.Errorf("scylla rule = %v, want 9042 on internal CIDR", rules[2])
	}
}

func TestRebuildSGDesiredState_DDCEdge(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.VPCId = "vpc-edge"
	cfg.DDC.PublicPort = 8088
	cfg.DDC.InternalPort = 9099

	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge, "vpcId": "vpc-edge-rec"},
	}
	m := rebuildSGDoc(t, rec, &state.ModuleState{Name: "ddc"}, cfg)
	if m["GroupName"] != "fabrica-ddc-sg-eu-west-1" {
		t.Errorf("expected edge group name, got %v", m["GroupName"])
	}
	// The recorded edge VPC wins; the home config VPC must not leak into an
	// edge group (it would point at the wrong region's VPC).
	if m["VpcId"] != "vpc-edge-rec" {
		t.Errorf("expected recorded edge VPC vpc-edge-rec, got %v", m["VpcId"])
	}
	rules := sgIngressRules(t, m)
	if len(rules) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(rules))
	}
	for _, r := range rules {
		if r["FromPort"].(float64) == 9042 {
			t.Errorf("edge SG must not open Scylla CQL: %v", r)
		}
	}
}

func TestRebuildSGDesiredState_Workstation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.VPCId = "vpc-ws"
	cfg.Workstation.AllowedCIDR = "10.0.0.0/16"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "workstation"}, cfg)
	if m["GroupName"] != "fabrica-workstation-sg" {
		t.Errorf("expected canonical group name, got %v", m["GroupName"])
	}
	rules := sgIngressRules(t, m)
	if len(rules) != 1 || rules[0]["FromPort"].(float64) != 8443 {
		t.Fatalf("expected 1 ingress rule (8443), got %v", m["SecurityGroupIngress"])
	}
}

func TestRebuildSGDesiredState_CISG(t *testing.T) {
	cfg := config.Defaults()
	cfg.CI.VPCId = "vpc-ci"

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "ci"}, cfg)
	if m["GroupName"] != "fabrica-ci-sg" {
		t.Errorf("expected CI group name, got %v", m["GroupName"])
	}
	if m["VpcId"] != "vpc-ci" {
		t.Errorf("expected VpcId vpc-ci, got %v", m["VpcId"])
	}
	rules := sgIngressRules(t, m)
	if len(rules) != 0 {
		t.Errorf("CI SG has no inbound rules, got %v", rules)
	}
}

// TestRebuildSGDesiredState_DefaultCIDRFallback covers the behavior change
// from the old rebuild: an empty config CIDR falls back to the module
// default (what create would have used) instead of omitting ingress.
func TestRebuildSGDesiredState_DefaultCIDRFallback(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.VPCId = "vpc-123"
	// AllowedCIDR left empty

	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "perforce"}, cfg)
	rules := sgIngressRules(t, m)
	if len(rules) != 1 {
		t.Fatalf("expected 1 ingress rule, got %v", m["SecurityGroupIngress"])
	}
	if rules[0]["CidrIp"] != "10.0.0.0/8" {
		t.Errorf("expected module default CIDR fallback, got %v", rules[0]["CidrIp"])
	}
}

func TestRebuildSGDesiredState_UnknownModule(t *testing.T) {
	cfg := config.Defaults()
	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "unknown"}, cfg)
	if m["GroupDescription"] != "Fabrica-managed security group" {
		t.Error("expected default description")
	}
	if _, has := m["VpcId"]; has {
		t.Error("expected no VpcId for unknown module")
	}
	if _, has := m["SecurityGroupIngress"]; has {
		t.Error("expected no ingress for unknown module")
	}
}

func TestRebuildSGDesiredState_UnknownModuleRecordedVPC(t *testing.T) {
	cfg := config.Defaults()
	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{"vpcId": "vpc-recorded"},
	}
	m := rebuildSGDoc(t, rec, &state.ModuleState{Name: "unknown"}, cfg)
	if m["VpcId"] != "vpc-recorded" {
		t.Errorf("expected recorded vpcId, got %v", m["VpcId"])
	}
}

func TestRebuildSGDesiredState_PropertiesOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-config"

	rec := &state.ModuleResource{
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{
			"vpcId": "vpc-recorded",
		},
	}

	m := rebuildSGDoc(t, rec, &state.ModuleState{Name: "horde"}, cfg)
	if m["VpcId"] != "vpc-recorded" {
		t.Errorf("expected recorded vpcId override, got %v", m["VpcId"])
	}
}

func TestRebuildSGDesiredState_NoVPCKnown(t *testing.T) {
	cfg := config.Defaults()
	// No VPC in config or recorded properties.
	m := rebuildSGDoc(t, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "horde"}, cfg)
	if _, has := m["VpcId"]; has {
		t.Errorf("expected VpcId omitted when unresolved, got %v", m["VpcId"])
	}
}

// withSGDispatch swaps the per-module SG dispatch seam for the duration of a
// test, restoring it on cleanup.
func withSGDispatch(t *testing.T, fn func(*state.ModuleResource, *state.ModuleState, *config.Config) (json.RawMessage, error)) {
	t.Helper()
	orig := moduleSGDispatch
	moduleSGDispatch = fn
	t.Cleanup(func() { moduleSGDispatch = orig })
}

func TestRebuildSGDesiredState_BuilderError(t *testing.T) {
	withSGDispatch(t, func(*state.ModuleResource, *state.ModuleState, *config.Config) (json.RawMessage, error) {
		return nil, errors.New("builder exploded")
	})
	_, err := rebuildSGDesiredState(
		&state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup},
		&state.ModuleState{Name: "horde"},
		config.Defaults(),
	)
	if err == nil || !strings.Contains(err.Error(), "builder exploded") {
		t.Fatalf("expected builder error to propagate, got %v", err)
	}
}

func TestRebuildResource_SGRebuildError(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
	})
	withSGDispatch(t, func(*state.ModuleResource, *state.ModuleState, *config.Config) (json.RawMessage, error) {
		return nil, errors.New("builder exploded")
	})

	dr := &DriftResult{
		Module:     "horde",
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Identifier: "sg-123",
	}
	_, err := rebuildResource(dr, st, config.Defaults())
	if err == nil {
		t.Fatal("expected rebuild error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "rebuild desired state for security group sg-123 in module horde") {
		t.Errorf("error should name the resource and module: %s", msg)
	}
}

// A builder failure degrades the plan action to a report-only skip with an
// operator-facing reason, instead of emitting a resource without state.
func TestPlanRemediation_SGRebuildFailureSkips(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
	})
	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "horde",
			Resources: []DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123", Status: Missing},
			},
		}},
		Missing: 1,
	}
	withSGDispatch(t, func(*state.ModuleResource, *state.ModuleState, *config.Config) (json.RawMessage, error) {
		return nil, errors.New("builder exploded")
	})

	plan := PlanRemediation(report, st, config.Defaults())
	if plan.ToFix != 0 || plan.ToSkip != 1 || plan.ReportOnly != 1 {
		t.Fatalf("expected 0 to fix / 1 to skip / 1 report-only, got %+v", plan)
	}
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	a := plan.Actions[0]
	if a.Kind != ActionSkip {
		t.Errorf("expected skip, got %s", a.Kind)
	}
	if a.Resource != nil {
		t.Error("skipped action must not carry a resource")
	}
	if !strings.Contains(a.Reason, "cannot rebuild") || !strings.Contains(a.Reason, "recreate manually via fabrica") {
		t.Errorf("skip reason should explain the failure and the remedy, got %q", a.Reason)
	}
}

func TestStripEmptyVPCID_InvalidJSON(t *testing.T) {
	if _, err := stripEmptyVPCID(json.RawMessage("{not-json")); err == nil {
		t.Fatal("expected unmarshal error for invalid JSON")
	}
}

// --- rebuildInstanceDesiredState per-module tests ---

func TestRebuildInstanceDesiredState_Perforce(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.InstanceType = "m5.xlarge"
	cfg.Perforce.SubnetId = "subnet-pf"

	mod := &state.ModuleState{Name: "perforce", Version: "2024.2"}
	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Properties: map[string]string{"instanceType": "m5.xlarge"},
	}

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m5.xlarge" {
		t.Errorf("expected m5.xlarge, got %v", m["InstanceType"])
	}
	if m["ImageId"] != "2024.2" {
		t.Errorf("expected ImageId 2024.2 (module version), got %v", m["ImageId"])
	}
	if m["SubnetId"] != "subnet-pf" {
		t.Errorf("expected SubnetId subnet-pf, got %v", m["SubnetId"])
	}
}

// TestRebuildInstanceDesiredState_HordeAgentSGIndependence verifies the
// coordinator instance rebuild pairs with the coordinator group, not the
// agent-pool group, when the agent group is recorded first in the module.
func TestRebuildInstanceDesiredState_HordeAgentSGIndependence(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.InstanceType = "m7i.2xlarge"
	cfg.Horde.AmiID = "ami-horde"
	cfg.Horde.SubnetId = "subnet-horde"

	mod := &state.ModuleState{
		Name:    "horde",
		Version: "ami-horde",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coord"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"},
		},
	}
	rec := &mod.Resources[2]

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	sgIDs, ok := m["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0].(string) != "sg-coord" {
		t.Errorf("expected the coordinator group sg-coord, got %v", m["SecurityGroupIds"])
	}
}

func TestRebuildInstanceDesiredState_Lore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.InstanceType = "m5.xlarge"
	cfg.Lore.AmiID = "ami-lore"
	cfg.Lore.SubnetId = "subnet-lore"

	mod := &state.ModuleState{Name: "lore", Version: "ami-lore"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2Instance}

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m5.xlarge" {
		t.Errorf("expected m5.xlarge, got %v", m["InstanceType"])
	}
}

func TestRebuildInstanceDesiredState_DDC(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.InstanceType = "m5.xlarge"
	cfg.DDC.AmiID = "ami-ddc"

	mod := &state.ModuleState{Name: "ddc", Version: ""}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2Instance}

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["ImageId"] != "ami-ddc" {
		t.Errorf("expected ImageId ami-ddc from config, got %v", m["ImageId"])
	}
}

func TestRebuildInstanceDesiredState_Workstation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.InstanceType = "g4dn.xlarge"
	cfg.Workstation.AmiID = "ami-ws"

	mod := &state.ModuleState{Name: "workstation", Version: ""}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2Instance}

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "g4dn.xlarge" {
		t.Errorf("expected g4dn.xlarge, got %v", m["InstanceType"])
	}
	if m["ImageId"] != "ami-ws" {
		t.Errorf("expected ImageId ami-ws, got %v", m["ImageId"])
	}
}

func TestRebuildInstanceDesiredState_SecurityGroupLink(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.InstanceType = "m7i.2xlarge"
	cfg.Horde.AmiID = "ami-fake"

	mod := &state.ModuleState{Name: "horde", Version: "ami-fake"}
	mod.Resources = []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-linked"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"},
	}
	rec := &mod.Resources[1]

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	sgIDs, ok := m["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 {
		t.Fatalf("expected SecurityGroupIds with 1 entry, got %v", m["SecurityGroupIds"])
	}
	if sgIDs[0].(string) != "sg-linked" {
		t.Errorf("expected sg-linked, got %v", sgIDs[0])
	}
}

func TestRebuildInstanceDesiredState_PropertiesOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.InstanceType = "m7i.2xlarge"

	mod := &state.ModuleState{Name: "horde", Version: "ami-fake"}
	rec := &state.ModuleResource{
		TypeName: cloud.TypeAWSEC2Instance,
		Properties: map[string]string{
			"instanceType": "m7i.4xlarge",
			"subnetId":     "subnet-recorded",
		},
	}

	ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m7i.4xlarge" {
		t.Errorf("expected recorded instanceType override, got %v", m["InstanceType"])
	}
	if m["SubnetId"] != "subnet-recorded" {
		t.Errorf("expected recorded subnetId override, got %v", m["SubnetId"])
	}
}

// --- rebuildResource tests ---

func TestRebuildResource_ModuleNotFound(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	cfg := config.Defaults()

	dr := &DriftResult{
		Module:     "nonexistent",
		TypeName:   cloud.TypeAWSEC2Instance,
		Identifier: "i-123",
	}

	res, err := rebuildResource(dr, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TypeName != cloud.TypeAWSEC2Instance {
		t.Errorf("expected typeName, got %s", res.TypeName)
	}
	if res.Identifier != "i-123" {
		t.Errorf("expected identifier, got %s", res.Identifier)
	}
	if res.DesiredState != nil {
		t.Error("expected nil desired state when module not found")
	}
}

func TestRebuildResource_ResourceNotFoundInModule(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
	})
	cfg := config.Defaults()

	dr := &DriftResult{
		Module:     "horde",
		TypeName:   cloud.TypeAWSEC2Instance,
		Identifier: "i-missing",
	}

	res, err := rebuildResource(dr, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DesiredState != nil {
		t.Error("expected nil desired state when resource not found in module")
	}
}

func TestRebuildResource_InstanceWithDesiredState(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})
	cfg := config.Defaults()
	cfg.Horde.InstanceType = "m7i.2xlarge"

	dr := &DriftResult{
		Module:     "horde",
		TypeName:   cloud.TypeAWSEC2Instance,
		Identifier: "i-123",
	}

	res, err := rebuildResource(dr, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DesiredState == nil {
		t.Fatal("expected non-nil desired state for instance")
	}
	var m map[string]any
	if err := json.Unmarshal(res.DesiredState, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m7i.2xlarge" {
		t.Errorf("expected m7i.2xlarge, got %v", m["InstanceType"])
	}
}

func TestRebuildResource_SGWithDesiredState(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
	})
	cfg := config.Defaults()
	cfg.Perforce.AllowedCIDR = "10.0.0.0/16"

	dr := &DriftResult{
		Module:     "perforce",
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Identifier: "sg-123",
	}

	res, err := rebuildResource(dr, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.DesiredState == nil {
		t.Fatal("expected non-nil desired state for SG")
	}
	var m map[string]any
	if err := json.Unmarshal(res.DesiredState, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	// Canonical perforce builder: group name + the p4d rule on the config CIDR.
	if m["GroupName"] != "fabrica-perforce-sg" {
		t.Errorf("unexpected group name: %v", m["GroupName"])
	}
	ingress, ok := m["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %v", m["SecurityGroupIngress"])
	}
	rule := ingress[0].(map[string]any)
	if rule["FromPort"].(float64) != 1666 || rule["CidrIp"] != "10.0.0.0/16" {
		t.Errorf("unexpected rule: %v", rule)
	}
}

// --- refreshSGIDs tests ---

func TestRefreshSGIDs_UpdatesInstanceAfterSG(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	instDS := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-old"]}`)

	// The SG action's Identifier is the recorded (old) id; the created
	// resource carries the new id the provider returned.
	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", DesiredState: instDS}},
		},
	}

	refreshSGIDs(plan, 0, "horde", "", "sg-old", "sg-new")

	var ds map[string]any
	if err := json.Unmarshal(plan.Actions[1].Resource.DesiredState, &ds); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	sgIDs, ok := ds["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0].(string) != "sg-new" {
		t.Errorf("expected SecurityGroupIds updated to sg-new, got %v", ds["SecurityGroupIds"])
	}
}

func TestRefreshSGIDs_SkipsDifferentModule(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	instDS := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-old"]}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "perforce", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-456", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-456", DesiredState: instDS}},
		},
	}

	refreshSGIDs(plan, 0, "horde", "", "sg-old", "sg-new")

	var ds map[string]any
	if err := json.Unmarshal(plan.Actions[1].Resource.DesiredState, &ds); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	sgIDs, ok := ds["SecurityGroupIds"].([]any)
	if !ok || sgIDs[0].(string) != "sg-old" {
		t.Error("expected SecurityGroupIds unchanged for different module")
	}
}

func TestRefreshSGIDs_SkipsNonInstance(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-other", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-other", DesiredState: sgDS}},
		},
	}

	// Should not panic when next action is not an instance.
	refreshSGIDs(plan, 0, "horde", "", "sg-old", "sg-new")
}

// TestRefreshSGIDs_PreservesOtherGroupIDs verifies the refresh is surgical:
// an instance attached to the recreated group AND another group keeps the
// other membership — only the old reference is replaced.
func TestRefreshSGIDs_PreservesOtherGroupIDs(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	instDS := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-old","sg-other"]}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "ddc", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", DesiredState: instDS}},
		},
	}

	refreshSGIDs(plan, 0, "ddc", "", "sg-old", "sg-new")

	var ds map[string]any
	if err := json.Unmarshal(plan.Actions[1].Resource.DesiredState, &ds); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	sgIDs, ok := ds["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 2 {
		t.Fatalf("expected 2 SecurityGroupIds, got %v", ds["SecurityGroupIds"])
	}
	if sgIDs[0].(string) != "sg-new" || sgIDs[1].(string) != "sg-other" {
		t.Errorf("expected [sg-new sg-other] (order preserved), got %v", sgIDs)
	}
}

// TestRefreshSGIDs_UnreferencedListUntouched verifies an instance whose
// group list does not reference the recreated group is left alone — the
// refresh never adds memberships the recorded plan did not have.
func TestRefreshSGIDs_UnreferencedListUntouched(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	original := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-unrelated"]}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "ddc", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", DesiredState: original}},
		},
	}

	refreshSGIDs(plan, 0, "ddc", "", "sg-old", "sg-new")

	if got := plan.Actions[1].Resource.DesiredState; string(got) != string(original) {
		t.Errorf("expected the instance desired state untouched, got %s", got)
	}
}

// --- canRecreate tests ---

func TestCanRecreate(t *testing.T) {
	tests := []struct {
		typeName string
		want     bool
	}{
		{cloud.TypeAWSEC2Instance, true},
		{cloud.TypeAWSEC2SecurityGroup, true},
		{"AWS::IAM::Role", false},
		{"AWS::CodeBuild::Project", false},
		{"AWS::S3::Bucket", false},
		{"", false},
	}
	for _, tc := range tests {
		got := canRecreate(tc.typeName)
		if got != tc.want {
			t.Errorf("canRecreate(%q) = %v, want %v", tc.typeName, got, tc.want)
		}
	}
}

// --- PlanRemediation for additional module SG types ---

func TestPlanRemediation_MissingSGPerModule(t *testing.T) {
	tests := []struct {
		module       string
		ami          string
		sgID         string
		expectedRule int
		setter       func(*config.Config)
	}{
		{
			module: "horde", ami: "ami-fake", sgID: "sg-123", expectedRule: 2,
			setter: func(c *config.Config) { c.Horde.AllowedCIDR = "10.0.0.0/16" },
		},
		{
			module: "lore", ami: "ami-lore", sgID: "sg-lore", expectedRule: 3,
			setter: func(c *config.Config) { c.Lore.AllowedCIDR = "10.0.0.0/16" },
		},
	}
	for _, tc := range tests {
		t.Run(tc.module, func(t *testing.T) {
			st := state.NewState("123456789012", "us-east-1")
			st.UpsertModule(tc.module, tc.ami, "ready", []state.ModuleResource{
				{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: tc.sgID},
			})

			report := &DriftReport{
				Modules: []ModuleDrift{{
					Name: tc.module,
					Resources: []DriftResult{
						{Module: tc.module, TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: tc.sgID, Status: Missing},
					},
				}},
				Missing: 1,
			}

			cfg := config.Defaults()
			tc.setter(cfg)
			plan := PlanRemediation(report, st, cfg)

			if plan.ToFix != 1 {
				t.Errorf("expected 1 to fix, got %d", plan.ToFix)
			}
			for _, a := range plan.Actions {
				if a.Kind == ActionCreate && a.Resource != nil {
					var ds map[string]any
					if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
						t.Fatalf("invalid desired state: %v", err)
					}
					ingress, ok := ds["SecurityGroupIngress"].([]any)
					if !ok || len(ingress) != tc.expectedRule {
						t.Errorf("expected %d ingress rules for %s, got %d", tc.expectedRule, tc.module, len(ingress))
					}
				}
			}
		})
	}
}

func TestPlanRemediation_MissingDDCInstance(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ddc"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: map[string]string{
			"instanceType": "m5.xlarge",
		}},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "ddc",
			Resources: []DriftResult{
				{Module: "ddc", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ddc", Status: InSync},
				{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Status: Missing},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	cfg.DDC.InstanceType = "m5.xlarge"
	cfg.DDC.AmiID = "ami-ddc"
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 1 {
		t.Errorf("expected 1 to fix, got %d", plan.ToFix)
	}
	for _, a := range plan.Actions {
		if a.Kind == ActionCreate && a.Resource != nil {
			var ds map[string]any
			if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
				t.Fatalf("invalid desired state: %v", err)
			}
			if ds["InstanceType"] != "m5.xlarge" {
				t.Errorf("expected m5.xlarge, got %v", ds["InstanceType"])
			}
			// Should have SG linked.
			sgIDs, ok := ds["SecurityGroupIds"].([]any)
			if !ok || len(sgIDs) != 1 {
				t.Error("expected SecurityGroupIds linked to sg-ddc")
			}
		}
	}
}

func TestPlanRemediation_MissingWorkstationInstance(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-ws", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ws"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ws", Properties: map[string]string{
			"instanceType": "g4dn.xlarge",
		}},
	})

	report := &DriftReport{
		Modules: []ModuleDrift{{
			Name: "workstation",
			Resources: []DriftResult{
				{Module: "workstation", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ws", Status: InSync},
				{Module: "workstation", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ws", Status: Missing},
			},
		}},
		Missing: 1,
	}

	cfg := config.Defaults()
	cfg.Workstation.InstanceType = "g4dn.xlarge"
	cfg.Workstation.AmiID = "ami-ws"
	plan := PlanRemediation(report, st, cfg)

	if plan.ToFix != 1 {
		t.Errorf("expected 1 to fix, got %d", plan.ToFix)
	}
	for _, a := range plan.Actions {
		if a.Kind == ActionCreate && a.Resource != nil {
			var ds map[string]any
			if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
				t.Fatalf("invalid desired state: %v", err)
			}
			if ds["InstanceType"] != "g4dn.xlarge" {
				t.Errorf("expected g4dn.xlarge, got %v", ds["InstanceType"])
			}
		}
	}
}
