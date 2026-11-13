package testutil_test

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	hordeami "github.com/jpvelasco/fabrica/cmd/horde/ami"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	loreami "github.com/jpvelasco/fabrica/cmd/lore/ami"
	workstationami "github.com/jpvelasco/fabrica/cmd/workstation/ami"
	"github.com/spf13/cobra"
)

func TestAMIRuntimeTests(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func(globals.RuntimeSource, io.Writer) *cobra.Command
	}{{"horde", hordeami.New}, {"lore", loreami.New}, {"workstation", workstationami.New}} {
		t.Run(factory.name, func(t *testing.T) {
			testutil.RunAMIRuntimeTests(t, factory.new)
		})
	}
}
