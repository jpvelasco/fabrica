package list_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/workstation/list"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(list.New(runtimeSource, optionsSource, out))
	return root
}

func runList(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"list"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime() globals.RuntimeSource {
	return testutil.NewNilProviderRuntime()
}

func TestListCobraNoneProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runList(t, newCobraRuntime())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	testutil.AssertContains(t, got, "No workstations provisioned")
}

func TestListCobraJSONNoneProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runList(t, newCobraRuntime(), "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v", err)
	}
	testutil.AssertContains(t, got, `"workstations"`)
}
