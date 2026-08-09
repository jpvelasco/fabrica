package drift

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
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
	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, r *cloud.Resource) error {
		created = append(created, r)
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
	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, r *cloud.Resource) error {
		callCount++
		if r.TypeName == cloud.TypeAWSEC2Instance {
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

	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, r *cloud.Resource) error {
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

	result := ApplyRemediation(context.Background(), plan, st, func(_ context.Context, r *cloud.Resource) error {
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

func TestRebuildSGDesiredState_Horde(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-123"
	cfg.Horde.AllowedCIDR = "10.0.0.0/16"

	mod := &state.ModuleState{Name: "horde"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["VpcId"] != "vpc-123" {
		t.Errorf("expected VpcId vpc-123, got %v", m["VpcId"])
	}
	ingress, ok := m["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) != 2 {
		t.Fatalf("expected 2 ingress rules, got %d", len(ingress))
	}
	rule0 := ingress[0].(map[string]any)
	if rule0["FromPort"].(float64) != 5000 {
		t.Errorf("expected port 5000, got %v", rule0["FromPort"])
	}
}

func TestRebuildSGDesiredState_Lore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.VPCId = "vpc-lore"
	cfg.Lore.AllowedCIDR = "10.0.0.0/16"

	mod := &state.ModuleState{Name: "lore"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	ingress, ok := m["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) != 3 {
		t.Fatalf("expected 3 ingress rules (gRPC tcp, QUIC udp, health tcp), got %d", len(ingress))
	}
}

func TestRebuildSGDesiredState_DDC(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.VPCId = "vpc-ddc"
	cfg.DDC.AllowedCIDR = "10.0.0.0/16"

	mod := &state.ModuleState{Name: "ddc"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	ingress, ok := m["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) != 2 {
		t.Fatalf("expected 2 ingress rules (80, 8080), got %d", len(ingress))
	}
}

func TestRebuildSGDesiredState_Workstation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.VPCId = "vpc-ws"
	cfg.Workstation.AllowedCIDR = "10.0.0.0/16"

	mod := &state.ModuleState{Name: "workstation"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	ingress, ok := m["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule (8443), got %d", len(ingress))
	}
	rule0 := ingress[0].(map[string]any)
	if rule0["FromPort"].(float64) != 8443 {
		t.Errorf("expected port 8443, got %v", rule0["FromPort"])
	}
}

func TestRebuildSGDesiredState_NoCIDR(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.VPCId = "vpc-123"
	// AllowedCIDR left empty

	mod := &state.ModuleState{Name: "perforce"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	// No ingress when CIDR is empty.
	if _, has := m["SecurityGroupIngress"]; has {
		t.Error("expected no SecurityGroupIngress when CIDR is empty")
	}
}

func TestRebuildSGDesiredState_UnknownModule(t *testing.T) {
	cfg := config.Defaults()
	mod := &state.ModuleState{Name: "unknown"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
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

func TestRebuildSGDesiredState_PropertiesOverride(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.VPCId = "vpc-config"

	mod := &state.ModuleState{Name: "horde"}
	rec := &state.ModuleResource{
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{
			"vpcId": "vpc-recorded",
		},
	}

	ds := rebuildSGDesiredState(rec, mod, cfg)
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["VpcId"] != "vpc-recorded" {
		t.Errorf("expected recorded vpcId override, got %v", m["VpcId"])
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

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

func TestRebuildInstanceDesiredState_Lore(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.InstanceType = "m5.xlarge"
	cfg.Lore.AmiID = "ami-lore"
	cfg.Lore.SubnetId = "subnet-lore"

	mod := &state.ModuleState{Name: "lore", Version: "ami-lore"}
	rec := &state.ModuleResource{TypeName: cloud.TypeAWSEC2Instance}

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

	ds := rebuildInstanceDesiredState(rec, mod, cfg)
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

	res := rebuildResource(dr, st, cfg)
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

	res := rebuildResource(dr, st, cfg)
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

	res := rebuildResource(dr, st, cfg)
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

	res := rebuildResource(dr, st, cfg)
	if res.DesiredState == nil {
		t.Fatal("expected non-nil desired state for SG")
	}
	var m map[string]any
	if err := json.Unmarshal(res.DesiredState, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["GroupDescription"] != "Fabrica-managed security group" {
		t.Errorf("unexpected description: %v", m["GroupDescription"])
	}
}

// --- refreshSGIDs tests ---

func TestRefreshSGIDs_UpdatesInstanceAfterSG(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	instDS := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-old"]}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", DesiredState: instDS}},
		},
	}

	refreshSGIDs(plan, 0, "horde", "sg-new")

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
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "perforce", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-456", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-456", DesiredState: instDS}},
		},
	}

	refreshSGIDs(plan, 0, "horde", "sg-new")

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
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-new", DesiredState: sgDS}},
			{Module: "horde", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-other", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-other", DesiredState: sgDS}},
		},
	}

	// Should not panic when next action is not an instance.
	refreshSGIDs(plan, 0, "horde", "sg-new")
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
