package status_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/lore/status"
)

func TestStatusCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(status.New(
		testutil.NewNilProviderRuntime(),
		optionsSource,
		&out,
	))
	got, err := testutil.RunCommandWithOut(t, root, &out, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}
