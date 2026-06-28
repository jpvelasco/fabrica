package ci_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(ci.New(runtimeSource, optionsSource, out))
	return root
}

func run(t *testing.T, src globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
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
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	return func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg, Provider: cobraProvider{}}, nil
	}
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
