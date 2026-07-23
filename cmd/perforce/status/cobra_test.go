package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/perforce/status"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

func runStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"status"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	return testutil.NewTestRuntime(provider)
}

// TestStatusCobraNotProvisioned verifies clean exit and message when no state exists.
func TestStatusCobraNotProvisioned(t *testing.T) {
	got, err := runStatus(t, newCobraRuntime(&testutil.CobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestStatusCobraJSONFlag verifies --json produces parseable JSON output.
func TestStatusCobraJSONFlag(t *testing.T) {
	got, err := runStatus(t, newCobraRuntime(&testutil.CobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result status.StatusOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.Provisioned {
		t.Error("expected provisioned=false when no state exists")
	}
}

// TestStatusCobraNilProviderExitsCleanly verifies nil provider does not panic.
func TestStatusCobraNilProvider(t *testing.T) {
	got, err := runStatus(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestStatusCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestStatusCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runStatus(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestStatusCobraWaitFlagAccepted verifies --wait/-w flag is accepted (no parse error).
// Behaviour (actual polling) is verified in white-box tests.
func TestStatusCobraWaitFlagAccepted(t *testing.T) {
	for _, flag := range []string{"--wait", "-w"} {
		t.Run(flag, func(t *testing.T) {
			// No state on disk → prints not-provisioned immediately, no real polling.
			t.Chdir(t.TempDir())
			_, err := runStatus(t, newCobraRuntime(&testutil.CobraFakeProvider{}), flag)
			if err != nil {
				t.Fatalf("%s flag caused error: %v", flag, err)
			}
		})
	}
}

// TestStatusCobraJSONProvisioned verifies --json output is valid JSON with provisioned=true
// when state exists on disk with a perforce module.
func TestStatusCobraJSONProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Write minimal state file
	stateJSON := `{
		"account":"123456789012","region":"us-east-1",
		"modules":[{"name":"perforce","version":"2024.2","status":"provisioning",
		"resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-cobra"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-cobra"}
		]}]}`
	testutil.WriteStateFile(t, dir, stateJSON)

	got, err := runStatus(t, newCobraRuntime(&testutil.CobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result status.StatusOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.Provisioned {
		t.Error("expected provisioned=true when state has perforce module")
	}
	if result.InstanceID != "i-cobra" {
		t.Errorf("instanceId = %q, want i-cobra", result.InstanceID)
	}
}
