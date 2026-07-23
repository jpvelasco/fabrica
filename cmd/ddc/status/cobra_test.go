package status_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

func TestCobraStatusNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	root, opts := testutil.BuildTestRoot(&buf)
	root.AddCommand(status.New(testutil.NewNilProviderRuntime(), func() globals.Options {
		return *opts
	}, &buf))
	root.SetArgs([]string{"status"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
