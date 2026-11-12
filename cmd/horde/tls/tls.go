// Package tls provides local migration artifacts and state-only TLS adoption.
package tls

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the migration command group.
func New(runtime globals.RuntimeSource, options globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "tls", Short: "Render and verify an operator-installed Horde TLS migration"}
	cmd.AddCommand(newRender(options, out), newAdopt(runtime, options, out))
	return cmd
}
