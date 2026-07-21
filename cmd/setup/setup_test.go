package setup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestSetupTagsMergesStandardAndUserTags(t *testing.T) {
	tags := setupTags("test-version", map[string]string{
		"Environment": "dev",
		"ManagedBy":   "custom",
	})

	if tags["Environment"] != "dev" {
		t.Fatalf("user tag not copied")
	}
	if tags["ManagedBy"] != "custom" {
		t.Fatalf("user tag should override standard tag, got %q", tags["ManagedBy"])
	}
	if tags["FabricaVersion"] != "test-version" {
		t.Fatalf("FabricaVersion = %q", tags["FabricaVersion"])
	}
}

func TestCostResourcesPreservesPlanLabels(t *testing.T) {
	resources := costResources([]fabricastate.ResourcePlan{
		{TypeName: "AWS::S3::Bucket", Label: "S3 bucket", Identifier: "state-bucket"},
	})

	if len(resources) != 1 {
		t.Fatalf("resource count = %d, want 1", len(resources))
	}
	if resources[0].TypeName != "AWS::S3::Bucket" {
		t.Fatalf("TypeName = %q", resources[0].TypeName)
	}
	if resources[0].Name != "S3 bucket (state-bucket)" {
		t.Fatalf("Name = %q", resources[0].Name)
	}
}

func TestAllResourcesExisted(t *testing.T) {
	if !allResourcesExisted([]fabricastate.BootstrapResult{{Name: "bucket", Existed: true}}) {
		t.Fatal("expected true when every resource existed")
	}
	if allResourcesExisted([]fabricastate.BootstrapResult{{Name: "bucket", Existed: false}}) {
		t.Fatal("expected false when any resource was created")
	}
}

func testApplyRuntime() globals.Runtime {
	cfg := config.Defaults()
	// Pre-set the account ID so saveAccountID is a no-op and the test never
	// touches the filesystem.
	cfg.Cloud.AWS.AccountID = "123456789012"
	return globals.Runtime{
		Provider: &fakeSetupProvider{},
		Config:   cfg,
	}
}

func okBootstrap(called *bool) func(context.Context, fabricac.Provider, *config.Config) ([]fabricastate.BootstrapResult, error) {
	return func(_ context.Context, _ fabricac.Provider, _ *config.Config) ([]fabricastate.BootstrapResult, error) {
		if called != nil {
			*called = true
		}
		return []fabricastate.BootstrapResult{
			{Name: "S3 bucket b", Existed: false},
			{Name: "DynamoDB table t", Existed: false},
		}, nil
	}
}

func TestRunApplyConfirmYesCreates(t *testing.T) {
	var buf strings.Builder
	var bootstrapCalled bool
	cmd := command{
		runtime:   testApplyRuntime(),
		out:       &buf,
		costs:     fabricacost.Global,
		bootstrap: okBootstrap(&bootstrapCalled),
		confirm:   func(string) bool { return true },
	}
	plan := fabricastate.SetupPlan{
		Account: "123456789012",
		Region:  "us-east-1",
		Backend: fabricastate.BackendNames{Bucket: "fabrica-state-123456789012", Table: "fabrica-state-lock"},
		Resources: []fabricastate.ResourcePlan{
			{TypeName: "AWS::S3::Bucket", Label: "S3 bucket", Identifier: "fabrica-state-123456789012"},
			{TypeName: "AWS::DynamoDB::Table", Label: "DynamoDB table", Identifier: "fabrica-state-lock"},
		},
	}

	if err := cmd.runApply(context.Background(), plan); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if !bootstrapCalled {
		t.Error("bootstrap should be called when confirmed")
	}
	out := buf.String()
	if !strings.Contains(out, "Setup complete") {
		t.Errorf("expected completion message, got:\n%s", out)
	}
	if !strings.Contains(out, "What just happened:") {
		t.Errorf("expected 'What just happened' recap, got:\n%s", out)
	}
	if !strings.Contains(out, "fabrica-state-123456789012") || !strings.Contains(out, "fabrica-state-lock") {
		t.Errorf("completion should name the bucket and table, got:\n%s", out)
	}
	if !strings.Contains(out, "Estimated cost:") {
		t.Errorf("completion should show estimated cost, got:\n%s", out)
	}
	if !strings.Contains(out, "fabrica status") || !strings.Contains(out, "fabrica perforce create") {
		t.Errorf("completion should guide toward status + provisioning, got:\n%s", out)
	}
	if !strings.Contains(out, "Run 'fabrica status' to see the current state of your studio infrastructure.") {
		t.Errorf("completion should close with the status nudge, got:\n%s", out)
	}
}

func TestRunApplyBootstrapErrorShowsRecovery(t *testing.T) {
	var buf strings.Builder
	cmd := command{
		runtime:   testApplyRuntime(),
		assumeYes: true,
		out:       &buf,
		bootstrap: func(context.Context, fabricac.Provider, *config.Config) ([]fabricastate.BootstrapResult, error) {
			return nil, errors.New("AccessDenied")
		},
		confirm: func(string) bool { return true },
	}
	plan := fabricastate.SetupPlan{
		Account: "123456789012",
		Region:  "us-east-1",
		Backend: fabricastate.BackendNames{Bucket: "b", Table: "t"},
	}

	err := cmd.runApply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected error")
	}
	out := buf.String()
	if !strings.Contains(out, "safe to recover") || !strings.Contains(out, "run 'fabrica setup' again") {
		t.Errorf("expected recovery guidance, got:\n%s", out)
	}
}

func TestRunApplyConfirmNoCancels(t *testing.T) {
	var buf strings.Builder
	var bootstrapCalled bool
	cmd := command{
		runtime:   testApplyRuntime(),
		out:       &buf,
		bootstrap: okBootstrap(&bootstrapCalled),
		confirm:   func(string) bool { return false },
	}
	plan := fabricastate.SetupPlan{Account: "123456789012", Region: "us-east-1"}

	if err := cmd.runApply(context.Background(), plan); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if bootstrapCalled {
		t.Error("bootstrap should NOT be called when declined")
	}
	if !strings.Contains(buf.String(), "Setup cancelled") {
		t.Errorf("expected cancellation message, got:\n%s", buf.String())
	}
}

func TestRunApplyAssumeYesSkipsConfirm(t *testing.T) {
	var buf strings.Builder
	confirmCalled := false
	cmd := command{
		runtime:   testApplyRuntime(),
		assumeYes: true,
		out:       &buf,
		bootstrap: okBootstrap(nil),
		confirm:   func(string) bool { confirmCalled = true; return true },
	}
	plan := fabricastate.SetupPlan{Account: "123456789012", Region: "us-east-1"}

	if err := cmd.runApply(context.Background(), plan); err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if confirmCalled {
		t.Error("confirm must be skipped when assumeYes is set")
	}
}

func TestRunApplyBootstrapErrorPropagates(t *testing.T) {
	var buf strings.Builder
	cmd := command{
		runtime:   testApplyRuntime(),
		assumeYes: true,
		out:       &buf,
		bootstrap: func(context.Context, fabricac.Provider, *config.Config) ([]fabricastate.BootstrapResult, error) {
			return nil, errors.New("boom")
		},
		confirm: func(string) bool { return true },
	}
	plan := fabricastate.SetupPlan{Account: "123456789012", Region: "us-east-1"}

	if err := cmd.runApply(context.Background(), plan); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestDryRunShowsRunWithoutDryRunHint(t *testing.T) {
	var buf strings.Builder
	cmd := command{
		costs:   fabricacost.Global,
		version: "test",
		out:     &buf,
	}
	plan := fabricastate.SetupPlan{
		Account: "123456789012",
		Region:  "us-east-1",
		Backend: fabricastate.BackendNames{
			Bucket: "fabrica-state-123456789012",
			Table:  "fabrica-state-lock",
		},
		Resources: []fabricastate.ResourcePlan{
			{TypeName: "AWS::S3::Bucket", Label: "S3 bucket", Identifier: "fabrica-state-123456789012"},
		},
	}

	cmd.printDryRun(plan, map[string]string{})

	out := buf.String()
	if !strings.Contains(out, "Run without --dry-run to create these resources.") {
		t.Errorf("expected dry-run hint, got:\n%s", out)
	}
	if strings.Contains(out, "not yet implemented") {
		t.Errorf("dry-run must not claim 'not yet implemented', got:\n%s", out)
	}
}

func TestSaveBackendConfigPopulatesEmptyFields(t *testing.T) {
	var buf strings.Builder
	cfg := config.Defaults()
	// Clear the fields that should be populated
	cfg.Cloud.AWS.AccountID = ""
	cfg.State.Bucket = ""
	cfg.State.Table = ""

	cmd := command{
		runtime: globals.Runtime{
			Config:     cfg,
			ConfigPath: "nonexistent-dir/fabrica.yaml", // won't actually save
		},
		out: &buf,
	}

	plan := fabricastate.NewSetupPlan(cfg, "111222333444", "us-west-2")

	cmd.saveBackendConfig(plan)

	if cfg.Cloud.AWS.AccountID != "111222333444" {
		t.Errorf("AccountID = %q, want 111222333444", cfg.Cloud.AWS.AccountID)
	}
	if cfg.State.Bucket != "fabrica-state-111222333444" {
		t.Errorf("Bucket = %q, want fabrica-state-111222333444", cfg.State.Bucket)
	}
	if cfg.State.Table != "fabrica-state-lock" {
		t.Errorf("Table = %q, want fabrica-state-lock", cfg.State.Table)
	}
	// Save will fail because path doesn't exist — should print warning
	if !strings.Contains(buf.String(), "Warning: could not save config") {
		t.Errorf("expected warning about failed save, got:\n%s", buf.String())
	}
}

func TestSaveBackendConfigSkipsWhenAllSet(t *testing.T) {
	var buf strings.Builder
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "already-set"
	cfg.State.Bucket = "my-bucket"
	cfg.State.Table = "my-table"

	cmd := command{
		runtime: globals.Runtime{
			Config: cfg,
		},
		out: &buf,
	}

	plan := fabricastate.NewSetupPlan(cfg, "different-account", "us-east-1")
	cmd.saveBackendConfig(plan)

	// Fields should be unchanged
	if cfg.Cloud.AWS.AccountID != "already-set" {
		t.Errorf("AccountID changed to %q, want already-set", cfg.Cloud.AWS.AccountID)
	}
	if cfg.State.Bucket != "my-bucket" {
		t.Errorf("Bucket changed to %q, want my-bucket", cfg.State.Bucket)
	}
	if buf.Len() > 0 {
		t.Errorf("expected no output when nothing to save, got:\n%s", buf.String())
	}
}

func TestSaveBackendConfigPartialFill(t *testing.T) {
	var buf strings.Builder
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "existing"
	cfg.State.Bucket = ""
	cfg.State.Table = "custom-table"

	cmd := command{
		runtime: globals.Runtime{
			Config:     cfg,
			ConfigPath: "nonexistent-dir/fabrica.yaml",
		},
		out: &buf,
	}

	plan := fabricastate.NewSetupPlan(cfg, "new-account", "eu-west-1")
	cmd.saveBackendConfig(plan)

	// AccountID unchanged, Table unchanged
	if cfg.Cloud.AWS.AccountID != "existing" {
		t.Errorf("AccountID changed to %q, want existing", cfg.Cloud.AWS.AccountID)
	}
	if cfg.State.Table != "custom-table" {
		t.Errorf("Table changed to %q, want custom-table", cfg.State.Table)
	}
	// Bucket should be filled from plan (which uses the plan's account)
	if cfg.State.Bucket != "fabrica-state-new-account" {
		t.Errorf("Bucket = %q, want fabrica-state-new-account", cfg.State.Bucket)
	}
}

type fakeSetupProvider struct{}

func (f *fakeSetupProvider) Name() string { return "fake" }
func (f *fakeSetupProvider) Identity(_ context.Context) (account, arn, region string, err error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeSetupProvider) Resources() fabricac.ResourceClient { return nil }
