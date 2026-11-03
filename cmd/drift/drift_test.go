package driftcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/drift"
	"github.com/jpvelasco/fabrica/internal/oplog"
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

// prepareHordeFixEnv isolates the test working directory and writes the
// horde credentials file the --fix rebuild seam reads from .fabrica/.
func prepareHordeFixEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteHordeCredentials(t, dir, "test-pass")
}

// hordeFixResources is the full recorded resource set a horde --fix
// instance rebuild requires: security group, instance, and IAM instance
// profile.
func hordeFixResources() []fabricastate.ModuleResource {
	return []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
			"rootDevice":   "/dev/sda1",
		}},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-horde-profile"},
	}
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
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

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

func TestRun_FixHeldLockFailsBeforeStateRead(t *testing.T) {
	lp := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}
	readCalls := 0
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.runtime.Provider = lp
		c.readState = func() (*fabricastate.State, error) {
			readCalls++
			return fabricastate.NewState("123456789012", "us-east-1"), nil
		}
	})
	err := c.run(context.Background(), false)
	if !errors.Is(err, provision.ErrStateLocked) {
		t.Fatalf("err = %v, want ErrStateLocked", err)
	}
	if lp.Acquires != 1 {
		t.Errorf("acquires = %d, want 1", lp.Acquires)
	}
	if readCalls != 0 {
		t.Errorf("state reads = %d, want 0 (read must wait for the lock)", readCalls)
	}
}

// TestRun_FixLockAcquiredBeforeStateRead is the stale-snapshot guard: the
// state read that --fix plans from happens after the lock is held, so the
// plan cannot be built on a snapshot another run already mutated.
func TestRun_FixLockAcquiredBeforeStateRead(t *testing.T) {
	lp := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}}
	acquiresAtRead := -1
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.runtime.Provider = lp
		c.readState = func() (*fabricastate.State, error) {
			acquiresAtRead = lp.Acquires
			return st, nil
		}
	})
	if err := c.run(context.Background(), false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if lp.Acquires != 1 {
		t.Fatalf("acquires = %d, want 1", lp.Acquires)
	}
	if acquiresAtRead < 1 {
		t.Errorf("acquires at state read = %d, want >= 1", acquiresAtRead)
	}
}

func TestRun_FixDryRunSkipsLock(t *testing.T) {
	lp := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}}
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.runtime.Provider = lp
	})
	if err := c.run(context.Background(), true); err != nil {
		t.Fatalf("run: %v", err)
	}
	if lp.Acquires != 0 {
		t.Errorf("dry-run acquires = %d, want 0", lp.Acquires)
	}
}

func TestRun_ReadOnlySkipsLock(t *testing.T) {
	lp := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}}
	c := newTestCommand(t, func(c *command) { c.runtime.Provider = lp })
	c.getResource = func(_ context.Context, _ *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	if err := c.run(context.Background(), false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if lp.Acquires != 0 {
		t.Errorf("read-only acquires = %d, want 0", lp.Acquires)
	}
}

func TestRun_FixApplySuccess(t *testing.T) {
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	var created []*cloud.Resource
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			created = append(created, a.Resource)
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
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			return errors.New("simulated failure")
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
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
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
		c.createResource = func(_ context.Context, _ *drift.RemediationAction) error { return nil }
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

func TestRun_FixJSONFailure(t *testing.T) {
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.jsonOut = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			return errors.New("simulated failure")
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
	if err == nil {
		t.Fatal("expected error on failure in JSON mode")
	}
	if !strings.Contains(err.Error(), "remediation failed") {
		t.Errorf("unexpected error: %v", err)
	}
	got := c.out.(*bytes.Buffer).String()
	var output map[string]any
	if err := json.Unmarshal([]byte(got), &output); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if _, ok := output["result"]; !ok {
		t.Error("expected 'result' in JSON output even on failure")
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
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge", Kind: drift.ActionCreate, Region: "eu-west-1"},
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
	if !strings.Contains(got, "(region eu-west-1)") {
		t.Errorf("expected the edge action's region suffix; got:\n%s", got)
	}
	if !strings.Contains(got, "[SKIP]") {
		t.Error("expected [SKIP]")
	}
	if !strings.Contains(got, "To fix:") {
		t.Error("expected 'To fix:'")
	}
}

func TestPlanActionAttrs(t *testing.T) {
	home := planActionAttrs(drift.RemediationAction{Module: "horde", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Kind: drift.ActionCreate})
	for _, a := range home {
		if a == "region" {
			t.Error("home action must not carry a region attribute")
		}
	}
	edge := planActionAttrs(drift.RemediationAction{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge", Kind: drift.ActionCreate, Region: "eu-west-1"})
	found := false
	for i := 0; i+1 < len(edge); i += 2 {
		if edge[i] == "region" && edge[i+1] == "eu-west-1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a region attribute pair for the edge action, got %v", edge)
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
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, _ *drift.RemediationAction) error { return nil }
		c.writeState = func(s *fabricastate.State) error {
			return errors.New("disk full")
		}
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			return cloud.ErrResourceNotFound
		}
		return nil
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

// --- oplog coverage for runFix ---

func logBuf(t *testing.T) *bytes.Buffer {
	t.Helper()
	oplog.ResetForTest()
	buf := &bytes.Buffer{}
	oplog.InitWithWriter(slog.LevelInfo, true, buf)
	return buf
}

func hasLog(t *testing.T, buf *bytes.Buffer, substr string) {
	t.Helper()
	if !strings.Contains(buf.String(), substr) {
		t.Errorf("expected log to contain %q, got:\n%s", substr, buf.String())
	}
}

func TestRunFix_NoDrift_Oplog(t *testing.T) {
	buf := logBuf(t)
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
	hasLog(t, buf, "no drift found, nothing to fix")
}

func TestRunFix_DryRun_Oplog(t *testing.T) {
	buf := logBuf(t)
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
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasLog(t, buf, "drift fix dry-run, showing plan")
	hasLog(t, buf, "plan action")
}

func TestRunFix_ConfirmReject_Oplog(t *testing.T) {
	buf := logBuf(t)
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
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasLog(t, buf, "drift fix aborted by user")
}

func TestRunFix_ConfirmAccept_Oplog(t *testing.T) {
	buf := logBuf(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	var created int
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = false
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.confirm = func(string) bool { return true }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			created++
			return nil
		}
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
	hasLog(t, buf, "drift fix confirmed by user")
}

func TestRunFix_AssumeYes_Oplog(t *testing.T) {
	buf := logBuf(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	var created int
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			created++
			return nil
		}
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
	hasLog(t, buf, "drift fix proceeding without confirmation")
}

func TestRunFix_FixPathLogMessages_Oplog(t *testing.T) {
	buf := logBuf(t)
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, _ *drift.RemediationAction) error { return nil }
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
	// A single successful fix run emits all of these debug messages.
	hasLog(t, buf, "plan action")
	hasLog(t, buf, "i-123")
	hasLog(t, buf, "applying drift remediation")
	hasLog(t, buf, "applied")
}

func TestRunFix_ResultMixed_Oplog(t *testing.T) {
	buf := logBuf(t)
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	var callCount int
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			callCount++
			// First action (SG) succeeds, second (instance) fails.
			if callCount == 1 {
				a.Resource.Identifier = "sg-new"
				return nil
			}
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
		t.Fatal("expected error on partial failure")
	}
	hasLog(t, buf, "applied")
	hasLog(t, buf, "failed")
}

func TestRunFix_WriteStateSuccess_Oplog(t *testing.T) {
	buf := logBuf(t)
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	var writeCalled bool
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, _ *drift.RemediationAction) error { return nil }
		c.writeState = func(s *fabricastate.State) error {
			writeCalled = true
			return nil
		}
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
	if !writeCalled {
		t.Error("expected writeState to be called")
	}
	hasLog(t, buf, "writing state after drift fix")
}

func TestRunFix_WriteStateError_Oplog(t *testing.T) {
	buf := logBuf(t)
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", hordeFixResources())

	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, _ *drift.RemediationAction) error { return nil }
		c.writeState = func(s *fabricastate.State) error {
			return errors.New("disk full")
		}
	})
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			return cloud.ErrResourceNotFound
		}
		return nil
	}
	c.listResources = func(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }

	err := c.run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	hasLog(t, buf, "failed to write state after drift fix")
}

func TestRunFix_SkippedActions_Oplog(t *testing.T) {
	buf := logBuf(t)
	prepareHordeFixEnv(t)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", append(hordeFixResources(), fabricastate.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Volume,
		Identifier: "vol-123",
	}))

	var callCount int
	c := newTestCommand(t, func(c *command) {
		c.fixMode = true
		c.assumeYes = true
		c.readState = func() (*fabricastate.State, error) { return st, nil }
		c.createResource = func(_ context.Context, a *drift.RemediationAction) error {
			callCount++
			// SG succeeds, instance fails — remaining actions (Volume ActionSkip)
			// are collected as skipped by ApplyRemediation.
			if callCount == 1 {
				a.Resource.Identifier = "sg-new"
				return nil
			}
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
		t.Fatal("expected error on partial failure")
	}
	hasLog(t, buf, "applied")
	hasLog(t, buf, "failed")
	hasLog(t, buf, "skipped")
}

// --- createInRegion / regionResource tests ---

// recordingRC is a minimal cloud.ResourceClient that records Create calls.
type recordingRC struct {
	created []string
}

func (r *recordingRC) Create(_ context.Context, res *cloud.Resource) error {
	r.created = append(r.created, res.Identifier)
	return nil
}

func (r *recordingRC) Get(_ context.Context, _ *cloud.Resource) error {
	return cloud.ErrResourceNotFound
}

func (r *recordingRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }

func (r *recordingRC) Delete(_ context.Context, _ *cloud.Resource) error {
	return nil
}

func (r *recordingRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func TestCreateInRegion_HomeActionUsesHomeClient(t *testing.T) {
	c := newTestCommand(t)
	homeRC := &recordingRC{}
	edgeRC := &recordingRC{}
	c.withRegion = func(_ context.Context, region string) (cloud.RegionView, error) {
		return cloud.RegionView{Resources: edgeRC}, nil
	}

	homeAction := &drift.RemediationAction{
		Region:   "",
		TypeName: cloud.TypeAWSEC2Instance,
		Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home"},
	}
	if err := c.createInRegion(context.Background(), homeRC, homeAction); err != nil {
		t.Fatalf("home create: %v", err)
	}
	// An action explicitly recorded in the configured home region also
	// routes through the home client.
	homeRegionAction := &drift.RemediationAction{
		Region:   config.DefaultAWSRegion,
		TypeName: cloud.TypeAWSEC2Instance,
		Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home-2"},
	}
	if err := c.createInRegion(context.Background(), homeRC, homeRegionAction); err != nil {
		t.Fatalf("home-region create: %v", err)
	}

	if len(homeRC.created) != 2 || len(edgeRC.created) != 0 {
		t.Errorf("expected both creates on the home client, got home=%v edge=%v", homeRC.created, edgeRC.created)
	}
}

func TestCreateInRegion_EdgeActionUsesRegionClient(t *testing.T) {
	c := newTestCommand(t)
	homeRC := &recordingRC{}
	edgeRC := &recordingRC{}
	c.withRegion = func(_ context.Context, region string) (cloud.RegionView, error) {
		if region != "eu-west-1" {
			return cloud.RegionView{}, fmt.Errorf("unexpected region %s", region)
		}
		return cloud.RegionView{Resources: edgeRC}, nil
	}

	a := &drift.RemediationAction{
		Region:   "eu-west-1",
		TypeName: cloud.TypeAWSEC2Instance,
		Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge"},
	}
	if err := c.createInRegion(context.Background(), homeRC, a); err != nil {
		t.Fatalf("edge create: %v", err)
	}
	if len(homeRC.created) != 0 || len(edgeRC.created) != 1 || edgeRC.created[0] != "i-edge" {
		t.Errorf("expected edge create on the region client only, got home=%v edge=%v", homeRC.created, edgeRC.created)
	}
}

func TestCreateInRegion_EdgeWithoutRegionSupport(t *testing.T) {
	c := newTestCommand(t)
	homeRC := &recordingRC{}
	// No withRegion seam: the provider does not support additional regions.

	a := &drift.RemediationAction{
		Region:   "eu-west-1",
		TypeName: cloud.TypeAWSEC2Instance,
		Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge"},
	}
	err := c.createInRegion(context.Background(), homeRC, a)
	if err == nil {
		t.Fatal("expected error for edge create without region support")
	}
	if !strings.Contains(err.Error(), "does not support additional regions") {
		t.Errorf("unexpected error: %v", err)
	}
	if len(homeRC.created) != 0 {
		t.Errorf("home client must not receive the edge create, got %v", homeRC.created)
	}
}

func TestCreateInRegion_RegionResolveError(t *testing.T) {
	c := newTestCommand(t)
	homeRC := &recordingRC{}
	c.withRegion = func(_ context.Context, region string) (cloud.RegionView, error) {
		return cloud.RegionView{}, fmt.Errorf("credential error for %s", region)
	}

	a := &drift.RemediationAction{
		Region:   "eu-west-1",
		TypeName: cloud.TypeAWSEC2Instance,
		Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge"},
	}
	err := c.createInRegion(context.Background(), homeRC, a)
	if err == nil || !strings.Contains(err.Error(), "resolving region eu-west-1") {
		t.Fatalf("expected wrapped resolve error, got %v", err)
	}
}

func TestRegionResource_Seam(t *testing.T) {
	c := newTestCommand(t)
	edgeRC := &recordingRC{}
	c.withRegion = func(_ context.Context, region string) (cloud.RegionView, error) {
		return cloud.RegionView{Resources: edgeRC}, nil
	}
	rc, err := c.regionResource(context.Background(), "eu-west-1")
	if err != nil {
		t.Fatalf("regionResource: %v", err)
	}
	if rc == nil {
		t.Fatal("expected non-nil region client")
	}

	c.withRegion = func(_ context.Context, region string) (cloud.RegionView, error) {
		return cloud.RegionView{}, fmt.Errorf("no credentials in %s", region)
	}
	if _, err := c.regionResource(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected error when the region seam fails")
	}
}
