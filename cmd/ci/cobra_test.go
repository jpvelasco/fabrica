package ci_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func run(t *testing.T, src globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root, opts := testutil.BuildTestRoot(&out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(ci.New(src, optionsSource, &out))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

type cobraProvider struct{}

func (cobraProvider) Name() string { return "fake" }
func (cobraProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-west-2", nil
}
func (cobraProvider) Resources() cloud.ResourceClient { return nil }

func cobraRuntime() globals.RuntimeSource {
	return testutil.NewTestRuntime(cobraProvider{})
}

func TestCISubcommandsRegistered(t *testing.T) {
	got, err := run(t, cobraRuntime(), "ci", "--help")
	if err != nil {
		t.Fatalf("ci --help: %v", err)
	}
	for _, sub := range []string{"setup", "trigger", "status", "logs"} {
		if !strings.Contains(got, sub) {
			t.Errorf("ci --help missing subcommand %q:\n%s", sub, got)
		}
	}
}

func TestCISetupDryRun(t *testing.T) {
	got, err := run(t, cobraRuntime(), "ci", "setup", "--dry-run")
	if err != nil {
		t.Fatalf("ci setup --dry-run: %v", err)
	}
	if !strings.Contains(got, "dry run") || !strings.Contains(got, "Cost estimate") {
		t.Errorf("expected dry-run plan + cost:\n%s", got)
	}
}

func TestCIStatusNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := run(t, cobraRuntime(), "ci", "status")
	if err != nil {
		t.Fatalf("ci status: %v", err)
	}
	if !strings.Contains(got, "not provisioned") {
		t.Errorf("expected not-provisioned:\n%s", got)
	}
}

func TestCIRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, errors.New("config not loaded") }
	if _, err := run(t, src, "ci", "setup", "--dry-run"); err == nil {
		t.Fatal("expected runtime error to surface")
	}
}

// TestCILogsAndTriggerWired exercises the logs/trigger New() wiring end-to-end.
// The fake provider does not implement CodeBuildRunner, so each command reaches
// its "does not support CodeBuild" / parse path without panicking.
func TestCILogsAndTriggerWired(t *testing.T) {
	if _, err := run(t, cobraRuntime(), "ci", "logs", "build-1"); err == nil {
		t.Error("expected error: provider lacks CodeBuildRunner")
	}
	// trigger with a nonexistent buildgraph file fails fast on parse.
	if _, err := run(t, cobraRuntime(), "ci", "trigger", "does-not-exist.xml"); err == nil {
		t.Error("expected error: missing buildgraph file")
	}
}
