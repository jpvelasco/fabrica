package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type actionContract struct {
	name          string
	spec          func() spec
	initialStatus string
	activeStatus  string
	targetStatus  string
	activeCode    string
	dryRunStatus  string
	successText   string
}

var actionContracts = []actionContract{
	{
		name:          "start",
		spec:          startSpec,
		initialStatus: "stopped",
		activeStatus:  "ready",
		targetStatus:  "ready",
		activeCode:    "already_running",
		dryRunStatus:  "would_start",
		successText:   "started",
	},
	{
		name:          "stop",
		spec:          stopSpec,
		initialStatus: "ready",
		activeStatus:  "stopped",
		targetStatus:  "stopped",
		activeCode:    "already_stopped",
		dryRunStatus:  "would_stop",
		successText:   "stopped",
	},
}

func TestSpecsDefineBothActionContracts(t *testing.T) {
	for _, contract := range actionContracts {
		t.Run(contract.name, func(t *testing.T) {
			commandSpec := contract.spec()
			if commandSpec.verb != contract.name {
				t.Fatalf("command verb = %q, want %q", commandSpec.verb, contract.name)
			}
			if commandSpec.short == "" || commandSpec.long == "" {
				t.Fatal("Cobra help text must not be empty")
			}
			if commandSpec.isAlreadyActive(contract.initialStatus) {
				t.Errorf("initial status %q must require the action", contract.initialStatus)
			}
			if !commandSpec.isAlreadyActive(contract.activeStatus) {
				t.Errorf("active status %q must be idempotent", contract.activeStatus)
			}
			if commandSpec.targetStatus != contract.targetStatus {
				t.Errorf("target status = %q, want %q", commandSpec.targetStatus, contract.targetStatus)
			}
			if got := commandSpec.alreadyActiveText("i-abc", contract.activeStatus); !strings.Contains(got, "i-abc") {
				t.Errorf("already-active text %q does not identify the instance", got)
			}
		})
	}

	if !startSpec().isAlreadyActive("provisioning") {
		t.Error("start must treat provisioning as already running")
	}
}

func TestRunInformationalPaths(t *testing.T) {
	for _, contract := range actionContracts {
		t.Run(contract.name, func(t *testing.T) {
			t.Run("not_provisioned_text", func(t *testing.T) {
				c, out := newTestCommand(contract.spec(), fabricastate.NewState("123456789012", "us-east-1"), nil)
				if err := c.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				testutil.AssertContains(t, out.String(), "Nothing to "+contract.name)
			})

			t.Run("not_provisioned_json", func(t *testing.T) {
				c, out := newTestCommand(contract.spec(), fabricastate.NewState("123456789012", "us-east-1"), func(opts *commandOptions) {
					opts.dryRun = true
					opts.jsonOut = true
				})
				if err := c.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				result := decodeActionOutput(t, out.Bytes())
				if result.Status != "not_provisioned" || !result.DryRun {
					t.Errorf("result = %+v", result)
				}
			})

			for _, jsonOut := range []bool{false, true} {
				name := "already_active_text"
				if jsonOut {
					name = "already_active_json"
				}
				t.Run(name, func(t *testing.T) {
					executeCalls := 0
					c, out := newTestCommand(contract.spec(), workstationState(contract.activeStatus), func(opts *commandOptions) {
						opts.jsonOut = jsonOut
						opts.executeAction = func(context.Context, string) error {
							executeCalls++
							return nil
						}
					})
					if err := c.run(context.Background()); err != nil {
						t.Fatal(err)
					}
					if executeCalls != 0 {
						t.Fatalf("execute calls = %d, want 0", executeCalls)
					}
					if jsonOut {
						result := decodeActionOutput(t, out.Bytes())
						if result.InstanceID != "i-abc" || result.Status != contract.activeCode {
							t.Errorf("result = %+v", result)
						}
						return
					}
					testutil.AssertContains(t, out.String(), "already")
				})
			}

			for _, jsonOut := range []bool{false, true} {
				name := "dry_run_text"
				if jsonOut {
					name = "dry_run_json"
				}
				t.Run(name, func(t *testing.T) {
					executeCalls := 0
					c, out := newTestCommand(contract.spec(), workstationState(contract.initialStatus), func(opts *commandOptions) {
						opts.dryRun = true
						opts.jsonOut = jsonOut
						opts.executeAction = func(context.Context, string) error {
							executeCalls++
							return nil
						}
					})
					if err := c.run(context.Background()); err != nil {
						t.Fatal(err)
					}
					if executeCalls != 0 {
						t.Fatalf("execute calls = %d, want 0", executeCalls)
					}
					if jsonOut {
						result := decodeActionOutput(t, out.Bytes())
						if result.Status != contract.dryRunStatus || !result.DryRun {
							t.Errorf("result = %+v", result)
						}
						return
					}
					testutil.AssertContains(t, out.String(), contract.name+" dry run")
					testutil.AssertContains(t, out.String(), contract.initialStatus)
				})
			}
		})
	}
}

func TestRunAppliesBothActions(t *testing.T) {
	testCases := []struct {
		name      string
		assumeYes bool
		jsonOut   bool
	}{
		{name: "yes_text", assumeYes: true},
		{name: "confirmed_text"},
		{name: "yes_json", assumeYes: true, jsonOut: true},
	}

	for _, contract := range actionContracts {
		t.Run(contract.name, func(t *testing.T) {
			for _, testCase := range testCases {
				t.Run(testCase.name, func(t *testing.T) {
					st := workstationState(contract.initialStatus)
					executedID := ""
					writeCalls := 0
					confirmCalls := 0
					c, out := newTestCommand(contract.spec(), st, func(opts *commandOptions) {
						opts.assumeYes = testCase.assumeYes
						opts.jsonOut = testCase.jsonOut
						opts.confirm = func(_, phrase string) bool {
							confirmCalls++
							return phrase == contract.name+" workstation i-abc"
						}
						opts.executeAction = func(_ context.Context, instanceID string) error {
							executedID = instanceID
							return nil
						}
						opts.writeState = func(got *fabricastate.State) error {
							writeCalls++
							if status := got.GetModule(moduleName).Status; status != contract.targetStatus {
								t.Errorf("persisted status = %q, want %q", status, contract.targetStatus)
							}
							return nil
						}
					})

					if err := c.run(context.Background()); err != nil {
						t.Fatal(err)
					}
					if executedID != "i-abc" || writeCalls != 1 {
						t.Fatalf("executed ID = %q, write calls = %d", executedID, writeCalls)
					}
					if testCase.assumeYes && confirmCalls != 0 {
						t.Errorf("confirmation calls = %d, want 0", confirmCalls)
					}
					if !testCase.assumeYes && confirmCalls != 1 {
						t.Errorf("confirmation calls = %d, want 1", confirmCalls)
					}
					if testCase.jsonOut {
						result := decodeActionOutput(t, out.Bytes())
						if result.InstanceID != "i-abc" || result.Status != contract.targetStatus || result.DryRun {
							t.Errorf("result = %+v", result)
						}
						return
					}
					testutil.AssertContains(t, out.String(), contract.successText)
				})
			}
		})
	}
}

func TestRunCancellationAndErrors(t *testing.T) {
	for _, contract := range actionContracts {
		t.Run(contract.name, func(t *testing.T) {
			t.Run("cancelled", func(t *testing.T) {
				executeCalls := 0
				c, out := newTestCommand(contract.spec(), workstationState(contract.initialStatus), func(opts *commandOptions) {
					opts.confirm = func(_, phrase string) bool {
						if phrase != contract.name+" workstation i-abc" {
							t.Errorf("confirmation phrase = %q", phrase)
						}
						return false
					}
					opts.executeAction = func(context.Context, string) error {
						executeCalls++
						return nil
					}
				})
				if err := c.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				if executeCalls != 0 {
					t.Errorf("execute calls = %d, want 0", executeCalls)
				}
				testutil.AssertContains(t, out.String(), "Cancelled")
			})

			t.Run("read_state", func(t *testing.T) {
				c, _ := newTestCommand(contract.spec(), nil, func(opts *commandOptions) {
					opts.readState = func() (*fabricastate.State, error) {
						return nil, errors.New("state unavailable")
					}
				})
				err := c.run(context.Background())
				if err == nil || !strings.Contains(err.Error(), "reading state: state unavailable") {
					t.Fatalf("error = %v", err)
				}
			})

			t.Run("missing_instance", func(t *testing.T) {
				st := fabricastate.NewState("123456789012", "us-east-1")
				st.UpsertModule(moduleName, "ami-123", contract.initialStatus, nil)
				c, _ := newTestCommand(contract.spec(), st, nil)
				err := c.run(context.Background())
				if err == nil || !strings.Contains(err.Error(), "has no instance in state") {
					t.Fatalf("error = %v", err)
				}
			})

			t.Run("no_executor", func(t *testing.T) {
				c, _ := newTestCommand(contract.spec(), workstationState(contract.initialStatus), func(opts *commandOptions) {
					opts.assumeYes = true
					opts.executeAction = nil
				})
				err := c.run(context.Background())
				if err == nil || !strings.Contains(err.Error(), "no provider configured") {
					t.Fatalf("error = %v", err)
				}
			})

			t.Run("execute", func(t *testing.T) {
				cause := errors.New("action failed")
				c, _ := newTestCommand(contract.spec(), workstationState(contract.initialStatus), func(opts *commandOptions) {
					opts.assumeYes = true
					opts.executeAction = func(context.Context, string) error { return cause }
				})
				err := c.run(context.Background())
				wantContext := strings.ToLower(contract.spec().progressText) + " instance i-abc"
				if !errors.Is(err, cause) || !strings.Contains(err.Error(), wantContext) {
					t.Fatalf("error = %v", err)
				}
			})

			t.Run("write_warning", func(t *testing.T) {
				c, out := newTestCommand(contract.spec(), workstationState(contract.initialStatus), func(opts *commandOptions) {
					opts.assumeYes = true
					opts.writeState = func(*fabricastate.State) error { return errors.New("disk full") }
				})
				if err := c.run(context.Background()); err != nil {
					t.Fatal(err)
				}
				testutil.AssertContains(t, out.String(), "Warning: could not update local state: disk full")
			})
		})
	}
}

func TestDefaultExecuteAction(t *testing.T) {
	t.Run("nil_provider", func(t *testing.T) {
		err := defaultExecuteAction(globals.Runtime{}, startVerb)(context.Background(), "i-abc")
		if err == nil || !strings.Contains(err.Error(), "no provider configured") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unsupported_provider", func(t *testing.T) {
		err := defaultExecuteAction(globals.Runtime{Provider: &fakeProvider{}}, startVerb)(context.Background(), "i-abc")
		if err == nil || !strings.Contains(err.Error(), "does not support EC2") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unknown_verb", func(t *testing.T) {
		err := defaultExecuteAction(globals.Runtime{Provider: &fakeEC2Manager{}}, "reboot")(context.Background(), "i-abc")
		if err == nil || !strings.Contains(err.Error(), "unknown action verb: reboot") {
			t.Fatalf("error = %v", err)
		}
	})

	manager := &fakeEC2Manager{}
	rt := globals.Runtime{Provider: manager}
	for _, verb := range []string{startVerb, stopVerb} {
		t.Run(verb, func(t *testing.T) {
			if err := defaultExecuteAction(rt, verb)(context.Background(), "i-abc"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if len(manager.startIDs) != 1 || manager.startIDs[0] != "i-abc" {
		t.Errorf("start IDs = %v", manager.startIDs)
	}
	if len(manager.stopIDs) != 1 || manager.stopIDs[0] != "i-abc" {
		t.Errorf("stop IDs = %v", manager.stopIDs)
	}
}

func workstationState(status string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule(moduleName, "ami-123", status, []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	return st
}

func newTestCommand(commandSpec spec, st *fabricastate.State, configure func(*commandOptions)) (*command, *bytes.Buffer) {
	out := &bytes.Buffer{}
	opts := commandOptions{
		out: out,
		confirm: func(string, string) bool {
			return true
		},
		readState: func() (*fabricastate.State, error) {
			return st, nil
		},
		writeState: func(*fabricastate.State) error {
			return nil
		},
		executeAction: func(context.Context, string) error {
			return nil
		},
	}
	if configure != nil {
		configure(&opts)
	}
	return newCommand(commandSpec, opts), out
}

func decodeActionOutput(t *testing.T, data []byte) ActionOutput {
	t.Helper()
	var result ActionOutput
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode output %q: %v", data, err)
	}
	return result
}

type fakeProvider struct{}

func (*fakeProvider) Name() string { return "fake" }
func (*fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (*fakeProvider) Resources() fabricac.ResourceClient { return nil }

type fakeEC2Manager struct {
	fakeProvider
	startIDs []string
	stopIDs  []string
}

func (f *fakeEC2Manager) StartInstance(_ context.Context, instanceID string) error {
	f.startIDs = append(f.startIDs, instanceID)
	return nil
}
func (f *fakeEC2Manager) StopInstance(_ context.Context, instanceID string) error {
	f.stopIDs = append(f.stopIDs, instanceID)
	return nil
}
