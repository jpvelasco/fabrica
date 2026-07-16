package destroy_test

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestCobraDestroyDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().BoolP("dry-run", "d", false, "")
	root.PersistentFlags().BoolP("yes", "y", false, "")
	root.PersistentFlags().BoolP("json", "j", false, "")
	rt := globals.Runtime{Config: &config.Config{}}
	root.AddCommand(destroy.New(func() (globals.Runtime, error) { return rt, nil }, func() globals.Options {
		return globals.Options{DryRun: true}
	}, io.Discard))
	root.SetArgs([]string{"destroy", "--dry-run"})
	// not provisioned — exits clean
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
