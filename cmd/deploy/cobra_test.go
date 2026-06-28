package deploy_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy"
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
	root.AddCommand(deploy.New(runtimeSource, optionsSource, out))
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

// cobraFakeProvider implements Provider + GameLiftManager so subcommand wiring
// can be exercised without the type assertion failing. Resources() returns a
// no-op client; individual command tests inject finer fakes via run().
type cobraFakeProvider struct{}

func (cobraFakeProvider) Name() string { return "fake" }
func (cobraFakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (cobraFakeProvider) Resources() cloud.ResourceClient                         { return nil }
func (cobraFakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (cobraFakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (cobraFakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}

func cobraRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "test-bucket"
	return func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg, Provider: cobraFakeProvider{}}, nil
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
