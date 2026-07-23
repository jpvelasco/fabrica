package submit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/submit"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(submit.New(runtimeSource, optionsSource, out))
	return root
}

func runSubmit(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"submit"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	return testutil.NewTestRuntime(provider)
}

func writeTempBuildGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSubmitCobraMissingArg verifies that omitting the positional arg produces a usage error.
func TestSubmitCobraMissingArg(t *testing.T) {
	_, err := runSubmit(t, newCobraRuntime(&testutil.CobraFakeProvider{}))
	if err == nil {
		t.Fatal("expected error when buildgraph-file arg is missing")
	}
}

// TestSubmitCobraWaitFlagAccepted verifies --wait/-w is accepted (no parse error).
func TestSubmitCobraWaitFlagAccepted(t *testing.T) {
	// No state on disk → command will fail with "not provisioned", but flag parsing succeeds.
	t.Chdir(t.TempDir())
	path := writeTempBuildGraph(t)
	for _, flag := range []string{"--wait", "-w"} {
		t.Run(flag, func(t *testing.T) {
			_, err := runSubmit(t, newCobraRuntime(&testutil.CobraFakeProvider{}), flag, path)
			// Error expected (not provisioned), but not a flag-parse error.
			if err != nil && err.Error() == "unknown flag: "+flag {
				t.Fatalf("%s flag not recognised", flag)
			}
		})
	}
}

// TestSubmitCobraNotProvisioned verifies error message when no state exists.
func TestSubmitCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	path := writeTempBuildGraph(t)
	_, err := runSubmit(t, newCobraRuntime(&testutil.CobraFakeProvider{}), path)
	if err == nil {
		t.Fatal("expected error when horde not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "not provisioned")
}

// TestSubmitCobraRuntimeError verifies runtimeSource error surfaces as command error.
func TestSubmitCobraRuntimeError(t *testing.T) {
	path := writeTempBuildGraph(t)
	runtimeSource := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not found")
	}
	_, err := runSubmit(t, runtimeSource, path)
	if err == nil {
		t.Fatal("expected error from runtimeSource")
	}
}
