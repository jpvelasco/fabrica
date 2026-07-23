package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/status"
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

// TestStatusCobraEmpty verifies a clean exit and setup hint when no state exists.
func TestStatusCobraEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStatus(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "fabrica setup") {
		t.Errorf("expected setup hint; got:\n%s", got)
	}
}

// TestStatusCobraJSON verifies --json produces a parseable StatusReport.
func TestStatusCobraJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStatus(t, testutil.NewNilProviderRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report status.StatusReport
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
}

// TestStatusCobraProbeFlagAccepted verifies --probe parses without error.
func TestStatusCobraProbeFlagAccepted(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runStatus(t, testutil.NewNilProviderRuntime(), "--probe"); err != nil {
		t.Fatalf("--probe caused error: %v", err)
	}
}

// TestStatusCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestStatusCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runStatus(t, src); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}
