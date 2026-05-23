package setup

import (
	"context"
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

func TestRunApplyPrintsNotImplementedWarning(t *testing.T) {
	var buf strings.Builder
	cmd := command{
		runtime: globals.Runtime{
			Provider: &fakeSetupProvider{},
			Config:   &config.Config{},
		},
		out: &buf,
		bootstrap: func(_ context.Context, _ fabricac.Provider, _ *config.Config) ([]fabricastate.BootstrapResult, error) {
			return nil, fabricastate.ErrBootstrapNotImplemented
		},
	}
	plan := fabricastate.SetupPlan{Account: "123456789012", Region: "us-east-1"}

	if err := cmd.runApply(context.Background(), plan); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "WARNING: fabrica setup is not yet functional") {
		t.Errorf("expected WARNING in output, got:\n%s", out)
	}
	if strings.Contains(out, "Setup complete") {
		t.Errorf("output must not contain 'Setup complete', got:\n%s", out)
	}
	if !strings.Contains(out, "docs/setup-manual.md") {
		t.Errorf("expected manual setup link in output, got:\n%s", out)
	}
}

func TestDryRunShowsNotImplementedNote(t *testing.T) {
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
	if !strings.Contains(out, "Automated provisioning is not yet implemented") {
		t.Errorf("expected NOT YET note in dry-run output, got:\n%s", out)
	}
	if strings.Contains(out, "Run without --dry-run to proceed") {
		t.Errorf("dry-run must not say 'Run without --dry-run' (implies it works), got:\n%s", out)
	}
}

type fakeSetupProvider struct{}

func (f *fakeSetupProvider) Name() string { return "fake" }
func (f *fakeSetupProvider) Identity(_ context.Context) (account, arn, region string, err error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeSetupProvider) Resources() fabricac.ResourceClient { return nil }
