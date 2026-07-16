package status_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestCobraStatusNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().BoolP("json", "j", false, "")
	root.PersistentFlags().BoolP("dry-run", "d", false, "")
	root.PersistentFlags().BoolP("yes", "y", false, "")
	rt := globals.Runtime{Config: &config.Config{}}
	root.AddCommand(status.New(func() (globals.Runtime, error) { return rt, nil }, func() globals.Options {
		return globals.Options{}
	}, &buf))
	root.SetArgs([]string{"status"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
