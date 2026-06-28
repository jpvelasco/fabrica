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
		bootstrap: okBootstrap(&bootstrapCalled),
		confirm:   func(string) bool { return true },
	}
	plan := fabricastate.SetupPlan{Account: "123456789012", Region: "us-east-1"}

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
	if !strings.Contains(out, "fabrica status") || !strings.Contains(out, "fabrica perforce create") {
		t.Errorf("completion should guide toward status + provisioning, got:\n%s", out)
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

type fakeSetupProvider struct{}

func (f *fakeSetupProvider) Name() string { return "fake" }
func (f *fakeSetupProvider) Identity(_ context.Context) (account, arn, region string, err error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeSetupProvider) Resources() fabricac.ResourceClient { return nil }
