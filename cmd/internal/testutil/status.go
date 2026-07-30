package testutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

// StatusTestSpec describes one module's status test configuration.
type StatusTestSpec struct {
	// ModuleName is the state module key (e.g. "horde", "perforce").
	ModuleName string
	// InstanceID is the EC2 instance identifier in the state fixture.
	InstanceID string
	// SGID is the security group identifier in the state fixture.
	SGID string
	// Version is the module version in the state fixture.
	Version string
	// Status is the module status in the state fixture.
	Status string
	// HasWaitFlag is true if the command supports --wait/-w.
	HasWaitFlag bool
	// NewCmd constructs the status subcommand.
	NewCmd func(globals.RuntimeSource, globals.OptionsSource, io.Writer) *cobra.Command
	// ParseJSONOutput parses the JSON output and returns (provisioned, instanceID, error).
	// This avoids importing the module-specific StatusOutput type.
	ParseJSONOutput func(raw string) (provisioned bool, instanceID string, err error)
}

// RunStatusCobraTests runs the standard suite of status cobra tests.
func RunStatusCobraTests(t *testing.T, spec StatusTestSpec) {
	t.Helper()

	if spec.Status == "" {
		spec.Status = "provisioning"
	}

	runCmd := func(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		root, optionsSource := BuildTestSubcommand(&out)
		root.AddCommand(spec.NewCmd(rt, optionsSource, &out))
		return RunCommandWithOut(t, root, &out, append([]string{"status"}, args...)...)
	}

	t.Run("NotProvisioned", func(t *testing.T) {
		got, err := runCmd(t, NewTestRuntime(&TestProvider{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		AssertContains(t, got, "not provisioned")
	})

	t.Run("JSONFlag", func(t *testing.T) {
		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		provisioned, _, err := spec.ParseJSONOutput(got)
		if err != nil {
			t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
		}
		if provisioned {
			t.Error("expected provisioned=false when no state exists")
		}
	})

	t.Run("NilProvider", func(t *testing.T) {
		got, err := runCmd(t, NewNilProviderRuntime())
		if err != nil {
			t.Fatalf("nil provider: unexpected error: %v", err)
		}
		AssertContains(t, got, "not provisioned")
	})

	t.Run("RuntimeError", func(t *testing.T) {
		src := func() (globals.Runtime, error) {
			return globals.Runtime{}, errors.New("config not loaded")
		}
		_, err := runCmd(t, src)
		if err == nil {
			t.Fatal("expected error when runtimeSource fails")
		}
	})

	if spec.HasWaitFlag {
		t.Run("WaitFlagAccepted", func(t *testing.T) {
			for _, flag := range []string{"--wait", "-w"} {
				t.Run(flag, func(t *testing.T) {
					t.Chdir(t.TempDir())
					_, err := runCmd(t, NewTestRuntime(&TestProvider{}), flag)
					if err != nil {
						t.Fatalf("%s flag caused error: %v", flag, err)
					}
				})
			}
		})
	}

	t.Run("JSONProvisioned", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)

		stateJSON := NewProvisionedStateJSON(StateModule{
			Name: spec.ModuleName, Version: spec.Version, Status: spec.Status,
			Resources: EC2Pair(spec.SGID, spec.InstanceID),
		})
		WriteStateFile(t, dir, stateJSON)

		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		provisioned, instanceID, err := spec.ParseJSONOutput(got)
		if err != nil {
			t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
		}
		if !provisioned {
			t.Error("expected provisioned=true when state has module")
		}
		if instanceID != spec.InstanceID {
			t.Errorf("instanceId = %q, want %q", instanceID, spec.InstanceID)
		}
	})
}

// ParseBaseStatusOutput is a default ParseJSONOutput implementation that
// unmarshals into modstatus.BaseStatusOutput. Works for any module whose
// StatusOutput embeds BaseStatusOutput at the top level.
func ParseBaseStatusOutput(raw string) (bool, string, error) {
	var result struct {
		Provisioned bool   `json:"provisioned"`
		InstanceID  string `json:"instanceId,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return false, "", err
	}
	return result.Provisioned, result.InstanceID, nil
}

// BuildTestRuntime is a convenience for status tests that need a runtime source.
func BuildTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	return NewTestRuntime(provider)
}
