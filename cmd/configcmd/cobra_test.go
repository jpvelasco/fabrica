package configcmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/configcmd"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(out)
	root.AddCommand(configcmd.New(runtimeSource, out))
	return root
}

func runConfig(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"config"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(accountID string) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = accountID
	cfg.Cloud.AWS.Region = "us-west-2"
	rt := globals.Runtime{Config: cfg}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestConfigShowCobra exercises the real Cobra entry for `config show`.
func TestConfigShowCobra(t *testing.T) {
	got, err := runConfig(t, newCobraRuntime("123456789012"), "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	for _, want := range []string{
		"Resolved resource names:",
		"S3 bucket:",
		"fabrica-state-123456789012",
		"DynamoDB table:",
		"fabrica-state-lock",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// Defaults YAML should surface the cloud provider.
	if !strings.Contains(got, "aws") && !strings.Contains(got, "provider") {
		// Accept either nested YAML keys from fileConfig(); at least region should appear.
		if !strings.Contains(got, "us-west-2") {
			t.Errorf("expected config YAML content; got:\n%s", got)
		}
	}
}

// TestConfigShowCobraRuntimeError verifies runtimeSource errors surface.
func TestConfigShowCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runConfig(t, src, "show"); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestConfigParentHelp verifies the parent `config` command is wired.
func TestConfigParentHelp(t *testing.T) {
	got, err := runConfig(t, newCobraRuntime(""), "--help")
	if err != nil {
		t.Fatalf("config --help: %v", err)
	}
	if !strings.Contains(got, "show") {
		t.Errorf("parent help should list show subcommand:\n%s", got)
	}
}
