package status_test

import (
	"bytes"
	"context"
	"strings"
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
	root.SetArgs([]string{"status"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), "not provisioned") {
		t.Fatalf("got %q", out.String())
	}
}
