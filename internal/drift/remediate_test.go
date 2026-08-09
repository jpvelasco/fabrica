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
