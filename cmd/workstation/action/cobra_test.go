package action_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/workstation/action"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

type cobraContract struct {
	name          string
	newCommand    func(globals.RuntimeSource, globals.OptionsSource, io.Writer) *cobra.Command
	initialStatus string
	activeStatus  string
	targetStatus  string
	dryRunText    string
	activeText    string
	successText   string
}

var cobraContracts = []cobraContract{
	{
		name:          "start",
		newCommand:    action.NewStart,
		initialStatus: "stopped",
		activeStatus:  "ready",
		targetStatus:  "ready",
		dryRunText:    "start dry run",
		activeText:    "already running",
		successText:   "started",
	},
	{
		name:          "stop",
		newCommand:    action.NewStop,
		initialStatus: "ready",
		activeStatus:  "stopped",
		targetStatus:  "stopped",
		dryRunText:    "stop dry run",
		activeText:    "already stopped",
		successText:   "stopped",
	},
}

func TestCobraActionContracts(t *testing.T) {
	for _, contract := range cobraContracts {
		t.Run(contract.name, func(t *testing.T) {
			t.Run("metadata", func(t *testing.T) {
				cmd := contract.newCommand(testutil.NewNilProviderRuntime(), func() globals.Options { return globals.Options{} }, io.Discard)
				if cmd.Use != contract.name || cmd.Short == "" || cmd.Long == "" {
					t.Errorf("command metadata = Use %q, Short %q, Long %q", cmd.Use, cmd.Short, cmd.Long)
				}
			})

			t.Run("runtime_error", func(t *testing.T) {
				runtimeErr := errors.New("config not loaded")
				runtimeSource := func() (globals.Runtime, error) {
					return globals.Runtime{}, runtimeErr
				}
				_, err := runAction(t, contract, runtimeSource)
				if !errors.Is(err, runtimeErr) {
					t.Fatalf("error = %v", err)
				}
			})

			t.Run("not_provisioned", func(t *testing.T) {
				t.Chdir(t.TempDir())
				got, err := runAction(t, contract, testutil.NewTestRuntime(&cobraFakeProvider{}))
				if err != nil {
					t.Fatal(err)
				}
				testutil.AssertContains(t, got, "not provisioned")
			})

			textCases := []struct {
				name      string
				status    string
				args      []string
				wantText  string
				wantCalls int
			}{
				{name: "dry_run", status: contract.initialStatus, args: []string{"--dry-run"}, wantText: contract.dryRunText},
				{name: "yes", status: contract.initialStatus, args: []string{"--yes"}, wantText: contract.successText, wantCalls: 1},
				{name: "already_active", status: contract.activeStatus, args: []string{"--yes"}, wantText: contract.activeText},
			}
			for _, testCase := range textCases {
				t.Run(testCase.name, func(t *testing.T) {
					provider := &cobraFakeProvider{}
					got, err := runProvisionedAction(t, contract, provider, testCase.status, testCase.args...)
					if err != nil {
						t.Fatal(err)
					}
					testutil.AssertContains(t, got, testCase.wantText)
					assertAPICalls(t, contract, provider, testCase.wantCalls)
				})
			}

			t.Run("json", func(t *testing.T) {
				provider := &cobraFakeProvider{}
				got, err := runProvisionedAction(t, contract, provider, contract.initialStatus, "--json", "--yes")
				if err != nil {
					t.Fatal(err)
				}
				var result action.ActionOutput
				if err := json.Unmarshal([]byte(got), &result); err != nil {
					t.Fatalf("decode output %q: %v", got, err)
				}
				if result.InstanceID != "i-cobrawstest" || result.Status != contract.targetStatus || result.DryRun {
					t.Errorf("result = %+v", result)
				}
				assertAPICalls(t, contract, provider, 1)
			})

			t.Run("provider_error", func(t *testing.T) {
				provider := &cobraFakeProvider{}
				cause := errors.New("EC2 unavailable")
				provider.setActionError(contract.name, cause)
				_, err := runProvisionedAction(t, contract, provider, contract.initialStatus, "--yes")
				if !errors.Is(err, cause) {
					t.Fatalf("error = %v", err)
				}
				assertAPICalls(t, contract, provider, 1)
			})
		})
	}
}

func runAction(t *testing.T, contract cobraContract, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(contract.newCommand(runtimeSource, optionsSource, &out))
	return testutil.RunCommandWithOut(t, root, &out, append([]string{contract.name}, args...)...)
}

func runProvisionedAction(t *testing.T, contract cobraContract, provider *cobraFakeProvider, status string, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON(status))
	return runAction(t, contract, testutil.NewTestRuntime(provider), args...)
}

func provisionedStateJSON(status string) string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name:      "workstation",
		Version:   "ami-test",
		Status:    status,
		Resources: testutil.EC2Pair("sg-cobrawstest", "i-cobrawstest"),
	})
}

func assertAPICalls(t *testing.T, contract cobraContract, provider *cobraFakeProvider, want int) {
	t.Helper()
	actionCalls, otherCalls := provider.calls(contract.name)
	if actionCalls != want {
		t.Errorf("%s calls = %d, want %d", contract.name, actionCalls, want)
	}
	if otherCalls != 0 {
		t.Errorf("other action calls = %d, want 0", otherCalls)
	}
}

type cobraFakeProvider struct {
	startCalls int
	stopCalls  int
	startErr   error
	stopErr    error
}

func (*cobraFakeProvider) Name() string { return "fake" }
func (*cobraFakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (*cobraFakeProvider) Resources() fabricac.ResourceClient { return nil }
func (f *cobraFakeProvider) StartInstance(context.Context, string) error {
	f.startCalls++
	return f.startErr
}
func (f *cobraFakeProvider) StopInstance(context.Context, string) error {
	f.stopCalls++
	return f.stopErr
}

func (f *cobraFakeProvider) calls(actionName string) (actionCalls, otherCalls int) {
	if actionName == "start" {
		return f.startCalls, f.stopCalls
	}
	return f.stopCalls, f.startCalls
}

func (f *cobraFakeProvider) setActionError(actionName string, err error) {
	if actionName == "start" {
		f.startErr = err
		return
	}
	f.stopErr = err
}
