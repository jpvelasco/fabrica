package status_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

func TestCobraStatusNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&buf)
	root.AddCommand(status.New(testutil.NewNilProviderRuntime(), optionsSource, &buf))
	if _, err := testutil.RunCommandWithOut(t, root, &buf, "status"); err != nil {
		t.Fatal(err)
	}
}
