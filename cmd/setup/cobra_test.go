package setup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/setup"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
	"github.com/spf13/cobra"
)

// buildTestRoot wires a minimal root replicating the persistent-flag hierarchy
// (--dry-run and --yes live on root, not on the subcommand).
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(setup.New(runtimeSource, optionsSource, out))
	return root
}

func runSetup(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"setup"}, args...)...)
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.State.Bucket = "fabrica-state-123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestSetupCobraDryRun verifies --dry-run prints the plan + cost and never bootstraps.
func TestSetupCobraDryRun(t *testing.T) {
	got, err := runSetup(t, newCobraRuntime(&testutil.BackendProvider{TestProvider: &testutil.TestProvider{}}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Setup (dry run)")
	testutil.AssertContains(t, got, "Cost estimate:")
	testutil.AssertContains(t, got, "Run without --dry-run to apply this plan.")
}

// TestSetupCobraYesApplies verifies --yes drives a successful apply with no prompt.
func TestSetupCobraYesApplies(t *testing.T) {
	t.Chdir(t.TempDir()) // saveAccountID is a no-op (account preset); guard any writes
	got, err := runSetup(t, newCobraRuntime(&testutil.BackendProvider{TestProvider: &testutil.TestProvider{}}), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Proceeding without confirmation")
	testutil.AssertContains(t, got, "Setup complete")
}

// TestSetupCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestSetupCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runSetup(t, src, "--dry-run"); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// Observe the actual row/context, then delegate all metadata mechanics.
type leaseObservedProvider struct {
	*testutil.BackendLockingProvider
	table, id string
	child     context.Context
	row       map[string]string
}

func (p *leaseObservedProvider) StateBucketMetadata(ctx context.Context, bucket string) (cloud.StateBackendMetadata, error) {
	if provision.StateLockHeld(ctx) {
		p.child, p.row = ctx, p.LockRow(p.table, p.id)
	}
	return p.BackendLockingProvider.StateBucketMetadata(ctx, bucket)
}

func TestSetupCobraRealLeaseAndOriginalConfig(t *testing.T) {
	for _, mode := range []string{"new", "existing", "nested"} {
		t.Run(mode, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cfg := config.Defaults()
			cfg.Cloud.AWS.AccountID, cfg.Cloud.AWS.Region = "", "us-west-2"
			cfg.State.Table = "chosen-locks"
			if err := os.WriteFile("fabrica.yaml", []byte("# studio\ncloud:\n  aws:\n    region: us-west-2\nstate:\n  table: chosen-locks\n"), 0600); err != nil {
				t.Fatal(err)
			}
			p := &leaseObservedProvider{BackendLockingProvider: &testutil.BackendLockingProvider{
				BackendProvider: &testutil.BackendProvider{TestProvider: &testutil.TestProvider{AccountID: "account-one", Region: "us-west-2"}}},
				table: cfg.State.Table, id: "fabrica-state/account-one"}
			if mode != "new" {
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
				p.Table = cloud.StateBackendMetadata{Exists: true, Tags: tags.Standard("setup", "old")}
			}
			rt := globals.Runtime{Config: cfg, Provider: p, ConfigPath: "fabrica.yaml"}
			func() {
				ctx := context.Background()
				if mode == "nested" {
					acquired, release, err := provision.AcquireStateLock(ctx, rt, "outer")
					if err != nil {
						t.Fatal(err)
					}
					defer release()
					ctx = acquired
				}
				var out bytes.Buffer
				root := buildTestRoot(func() (globals.Runtime, error) { return rt, nil }, &out)
				root.SetArgs([]string{"setup", "--yes"})
				if err := root.ExecuteContext(ctx); err != nil {
					t.Fatalf("factory setup: %v\n%s", err, out.String())
				}
				if p.child == nil || p.child == context.Background() || p.row["LockID"] != p.id || p.row["Token"] == "" {
					t.Fatalf("factory missed real target/row/child: %v %v", p.child, p.row)
				}
				if mode == "nested" && (p.child != ctx || !provision.StateLockHeld(ctx)) {
					t.Fatal("nested setup lost its actual outer acquisition")
				}
				want := "Setup complete"
				if mode != "new" {
					want = "State backend verified and configured"
				}
				testutil.AssertContains(t, out.String(), want)
			}()
			if len(p.LockRow(p.table, p.id)) != 0 {
				t.Fatal("factory failed to release its actual row")
			}
			acquires, releases := 0, 0
			for _, call := range p.BackendCalls {
				if call.Operation == "acquire row" {
					acquires++
					if call.Identifier != p.table+"/"+p.id {
						t.Fatalf("wrong actual table/lock ID: %+v", call)
					}
				}
				if call.Operation == "release row" {
					releases++
				}
				if call.Operation == "put document" {
					t.Fatal("default setup wrote canonical state")
				}
			}
			if acquires != 1 || releases != 1 {
				t.Fatalf("duplicate lease ownership: acquired %d/released %d", acquires, releases)
			}
			data, err := os.ReadFile("fabrica.yaml")
			if err != nil || !strings.Contains(string(data), "# studio") || cfg.Cloud.AWS.AccountID != "account-one" ||
				cfg.State.Bucket != state.DefaultBucket("account-one") || cfg.State.Table != p.table {
				t.Fatalf("original runtime/config not saved surgically: %s %v", data, err)
			}
			if _, err := os.Stat(".fabrica"); !os.IsNotExist(err) {
				t.Fatalf("default setup created a cache: %v", err)
			}
		})
	}
}
