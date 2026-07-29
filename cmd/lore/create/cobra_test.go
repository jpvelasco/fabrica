package create_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/lore/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"create"}, args...)...)
}

func newCobraTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	cfg.Lore.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "dry run")
}

func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.CreateCalls != 2 {
		t.Fatalf("--yes: expected 2 create calls, got %d", provider.CreateCalls)
	}
}

func TestCreateCobraNilProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	_, err := runCreate(t, func() (globals.Runtime, error) { return rt, nil })
	if err == nil {
		t.Fatal("expected error")
	}
	testutil.AssertContains(t, err.Error(), "no provider configured")
}
