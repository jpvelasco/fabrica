package testutil

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/destroy"
	"github.com/spf13/cobra"
)

// TestRunTeardownCobraTests exercises the shared teardown cobra suite against
// a real module constructor (horde destroy). Keeps RunTeardownCobraTests from
// shipping at 0% coverage and validates the suite still works end-to-end.
func TestRunTeardownCobraTests(t *testing.T) {
	RunTeardownCobraTests(t, TeardownTestSpec{
		ModuleName:      "horde",
		Verb:            "destroy",
		Version:         "ami-test",
		ExpectedDeletes: 2,
		Resources:       EC2Pair("sg-cobra123", "i-cobra123"),
		SuccessVerb:     "destroyed",
		NewCmd: func(rt globals.RuntimeSource, opts globals.OptionsSource, out io.Writer) *cobra.Command {
			return destroy.New(rt, opts, out)
		},
		NewTeardown: func(rt globals.Runtime, out io.Writer) any {
			return destroy.NewTeardown(rt, out)
		},
	})
}
