package deploy_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(deploy.New(runtimeSource, optionsSource, out))
	return root
}

func run(t *testing.T, src globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	return testutil.RunCommandWithOut(t, root, &out, args...)
}

func cobraRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "test-bucket"
	return func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg, Provider: &testutil.GameLiftProvider{}}, nil
	}
}

func TestDeploySubcommandsRegistered(t *testing.T) {
	got, err := run(t, cobraRuntime(), "deploy", "--help")
	if err != nil {
		t.Fatalf("deploy --help: %v", err)
	}
	for _, sub := range []string{"setup", "promote", "rollback", "status", "destroy"} {
		if !strings.Contains(got, sub) {
			t.Errorf("deploy --help missing subcommand %q:\n%s", sub, got)
		}
	}
}

func TestDeploySetupDryRun(t *testing.T) {
	got, err := run(t, cobraRuntime(), "deploy", "setup", "--dry-run")
	if err != nil {
		t.Fatalf("deploy setup --dry-run: %v", err)
	}
	if !strings.Contains(got, "dry run") || !strings.Contains(got, "Cost estimate") {
		t.Errorf("expected dry-run plan + cost:\n%s", got)
	}
}
