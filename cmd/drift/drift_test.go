package driftcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/drift"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(t *testing.T, opts ...func(*command)) *command {
	t.Helper()
	var out bytes.Buffer
	c := &command{
		runtime: globals.Runtime{
			Config: config.Defaults(),
		},
		out: &out,
		readState: func() (*fabricastate.State, error) {
			return fabricastate.NewState("123456789012", "us-east-1"), nil
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func TestRun_ReadStateError(t *testing.T) {
	c := newTestCommand(t, func(c *command) {
		c.readState = func() (*fabricastate.State, error) {
			return nil, errors.New("state file missing")
		}
	})
	err := c.run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_ReadOnlyText(t *testing.T) {
	c := newTestCommand(t)
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "Fabrica drift detection") {
		t.Errorf("expected header in output; got:\n%s", got)
	}
	if !strings.Contains(got, "No modules provisioned") {
		t.Errorf("expected 'No modules provisioned'; got:\n%s", got)
	}
}

func TestRun_ReadOnlyJSON(t *testing.T) {
	c := newTestCommand(t, func(c *command) {
		c.jsonOut = true
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
}

func TestRun_FixNoDrift(t *testing.T) {
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "No drift found") {
		t.Errorf("expected 'No drift found'; got:\n%s", got)
	}
}

func TestRun_FixDryRun(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			return cloud.ErrResourceNotFound
		}
		return nil
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "--dry-run") {
		t.Errorf("expected dry-run header; got:\n%s", got)
	}
	if !strings.Contains(got, "[FIX]") {
		t.Errorf("expected [FIX] action; got:\n%s", got)
	}
}

func TestRun_FixApplySuccess(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	var created []*cloud.Resource
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, r *cloud.Resource) error {
			created = append(created, r)
			return nil
		}
		c.writeState = func(s *fabricastate.State) error { return nil }
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			return cloud.ErrResourceNotFound
		}
		return nil
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "Drift remediation result") {
		t.Errorf("expected result header; got:\n%s", got)
	}
	if len(created) != 1 {
		t.Errorf("expected 1 create call, got %d", len(created))
	}
}

func TestRun_FixApplyFailure(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, r *cloud.Resource) error {
			return errors.New("simulated failure")
		}
		c.writeState = func(s *fabricastate.State) error { return nil }
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error on failure")
	}
	if !strings.Contains(err.Error(), "remediation failed") {
		t.Errorf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("expected [FAIL] in output; got:\n%s", got)
	}
}

func TestRun_FixConfirmReject(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = false
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.confirm = func(string) bool { return false }
		c.createResource = func(_ context.Context, r *cloud.Resource) error {
			t.Fatal("create should not be called when confirm rejected")
			return nil
		}
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "Aborted") {
		t.Errorf("expected 'Aborted'; got:\n%s", got)
	}
}

func TestRun_FixJSONNoDrift(t *testing.T) {
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.jsonOut = true
		c.assumeYes = true
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var output map[string]any
	if err := json.Unmarshal([]byte(got), &output); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if _, ok := output["plan"]; !ok {
		t.Error("expected 'plan' in JSON output")
	}
}

func TestRun_FixJSONWithResult(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.jsonOut = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, r *cloud.Resource) error { return nil }
		c.writeState = func(s *fabricastate.State) error { return nil }
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var output map[string]any
	if err := json.Unmarshal([]byte(got), &output); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if _, ok := output["plan"]; !ok {
		t.Error("expected 'plan' in JSON output")
	}
	if _, ok := output["result"]; !ok {
		t.Error("expected 'result' in JSON output")
	}
}

// --- printText tests ---

func TestPrintText_WithModules(t *testing.T) {
	c := newTestCommand(t)
	report := &drift.DriftReport{
		Backend: drift.DriftBackend{
			Bucket:       "fabrica-state-123",
			BucketStatus: drift.InSync,
			Table:        "fabrica-state-lock",
			TableStatus:  drift.InSync,
		},
		Modules: []drift.ModuleDrift{{
			Name: "horde",
			Resources: []drift.DriftResult{
				{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: drift.Missing, Details: "not found"},
			},
		}},
		Checked: 1,
		Missing: 1,
	}
	c.printText(report)
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "horde") {
		t.Errorf("expected 'horde'; got:\n%s", got)
	}
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("expected [FAIL]; got:\n%s", got)
	}
	if !strings.Contains(got, "not found") {
		t.Errorf("expected detail; got:\n%s", got)
	}
	if !strings.Contains(got, "Missing:  1") {
		t.Errorf("expected missing count; got:\n%s", got)
	}
}

func TestPrintText_MultipleModules(t *testing.T) {
	c := newTestCommand(t)
	report := &drift.DriftReport{
		Modules: []drift.ModuleDrift{
			{Name: "horde", Resources: []drift.DriftResult{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-1", Status: drift.InSync}}},
			{Name: "perforce", Resources: []drift.DriftResult{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-2", Status: drift.Mismatch, Details: "type mismatch"}}},
		},
		Checked:  2,
		InSync:   1,
		Mismatch: 1,
	}
	c.printText(report)
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "horde") {
		t.Error("expected 'horde'")
	}
	if !strings.Contains(got, "perforce") {
		t.Error("expected 'perforce'")
	}
	if !strings.Contains(got, "Mismatch:") {
		t.Error("expected 'Mismatch:' in summary")
	}
}

func TestPrintText_ExtraAndErrors(t *testing.T) {
	c := newTestCommand(t)
	report := &drift.DriftReport{
		Modules: []drift.ModuleDrift{{
			Name: "horde",
			Resources: []drift.DriftResult{
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-extra", Status: drift.Extra},
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-err", Status: drift.Error, Details: "no client"},
			},
		}},
		Checked: 2,
		Extra:   1,
		Errors:  1,
	}
	c.printText(report)
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "Extra:") {
		t.Error("expected 'Extra:' in summary")
	}
	if !strings.Contains(got, "Errors:") {
		t.Error("expected 'Errors:' in summary")
	}
}

// --- printBackend tests ---

func TestPrintBackend_Full(t *testing.T) {
	c := newTestCommand(t)
	c.printBackend(drift.DriftBackend{
		Bucket:        "bucket-123",
		BucketStatus:  drift.InSync,
		BucketDetails: "versioning enabled",
		Table:         "table-123",
		TableStatus:   drift.InSync,
		TableDetails:  "billing paid",
	})
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "bucket-123") {
		t.Error("expected bucket name")
	}
	if !strings.Contains(got, "versioning enabled") {
		t.Error("expected bucket details")
	}
	if !strings.Contains(got, "table-123") {
		t.Error("expected table name")
	}
}

func TestPrintBackend_Missing(t *testing.T) {
	c := newTestCommand(t)
	c.printBackend(drift.DriftBackend{
		Bucket:        "bucket-123",
		BucketStatus:  drift.Missing,
		BucketDetails: "bucket not found",
	})
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "[FAIL]") {
		t.Error("expected [FAIL] for missing bucket")
	}
}

// --- printFixPlan tests ---

func TestPrintFixPlan(t *testing.T) {
	c := newTestCommand(t)
	plan := &drift.RemediationPlan{
		Actions: []drift.RemediationAction{
			{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: drift.ActionCreate},
			{Module: "horde", TypeName: "AWS::CodeBuild::Project", Identifier: "cb-1", Kind: drift.ActionSkip, Reason: "unsupported"},
		},
		ToFix:  1,
		ToSkip: 1,
	}
	c.printFixPlan(plan)
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "[FIX]") {
		t.Error("expected [FIX]")
	}
	if !strings.Contains(got, "[SKIP]") {
		t.Error("expected [SKIP]")
	}
	if !strings.Contains(got, "To fix:") {
		t.Error("expected 'To fix:'")
	}
}

// --- printFixResult tests ---

func TestPrintFixResult_AllPaths(t *testing.T) {
	c := newTestCommand(t)
	result := &drift.RemediationResult{
		Applied: []drift.RemediationAction{{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"}},
		Skipped: []drift.RemediationAction{{Module: "horde", TypeName: "AWS::CodeBuild::Project", Identifier: "cb-1", Reason: "unsupported"}},
		Failed:  []drift.RemediationAction{{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-456"}},
		Errors:  []string{"simulated failure"},
	}
	plan := &drift.RemediationPlan{ToFix: 2, ToSkip: 1}
	c.printFixResult(result, plan)
	got := c.out.(*bytes.Buffer).String()
	if !strings.Contains(got, "[OK]") {
		t.Error("expected [OK]")
	}
	if !strings.Contains(got, "[SKIP]") {
		t.Error("expected [SKIP]")
	}
	if !strings.Contains(got, "[FAIL]") {
		t.Error("expected [FAIL]")
	}
	if !strings.Contains(got, "simulated failure") {
		t.Error("expected error message")
	}
}

// --- statusSymbol tests ---

func TestStatusSymbol(t *testing.T) {
	tests := []struct {
		status drift.DriftStatus
		want   string
	}{
		{drift.InSync, "[OK]"},
		{drift.Missing, "[FAIL]"},
		{drift.Extra, "[WARN]"},
		{drift.Mismatch, "[WARN]"},
		{drift.Error, "[????]"},
		{drift.DriftStatus("unknown"), "[????]"},
	}
	for _, tc := range tests {
		got := statusSymbol(tc.status)
		if !strings.Contains(got, tc.want) {
			t.Errorf("statusSymbol(%v) = %q, want to contain %q", tc.status, got, tc.want)
		}
	}
}

// --- printJSON test ---

func TestPrintJSON_Valid(t *testing.T) {
	c := newTestCommand(t)
	report := &drift.DriftReport{
		Modules: []drift.ModuleDrift{{
			Name: "horde",
			Resources: []drift.DriftResult{
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Status: drift.InSync},
			},
		}},
	}
	err := c.printJSON(report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

// --- printFixJSON tests ---

func TestPrintFixJSON_NoResult(t *testing.T) {
	c := newTestCommand(t)
	plan := &drift.RemediationPlan{ToFix: 0}
	err := c.printFixJSON(nil, plan, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := out["plan"]; !ok {
		t.Error("expected 'plan' field")
	}
}

func TestPrintFixJSON_WithResult(t *testing.T) {
	c := newTestCommand(t)
	result := &drift.RemediationResult{
		Applied: []drift.RemediationAction{{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123"}},
	}
	plan := &drift.RemediationPlan{ToFix: 1}
	err := c.printFixJSON(result, plan, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := out["result"]; !ok {
		t.Error("expected 'result' field")
	}
}

// --- writeState error path ---

func TestRun_WriteStateError(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, r *cloud.Resource) error { return nil }
		c.writeState = func(s *fabricastate.State) error {
			return errors.New("disk full")
		}
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "writing state after fix") {
		t.Errorf("unexpected error: %v", err)
	}
}
