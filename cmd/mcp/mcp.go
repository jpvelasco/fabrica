// Package mcp implements `fabrica mcp`: an MCP (Model Context Protocol) server
// that exposes read-only Fabrica tools over stdio transport. The same business
// logic used by the CLI is reused here — no duplicated AWS paths.
package mcp

import (
	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// New returns the "fabrica mcp" command.
// optionsSource is accepted for signature consistency with other commands but
// is not currently wired to any flag.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the Fabrica MCP server (stdio transport)",
		Long: `Run the Fabrica MCP (Model Context Protocol) server over stdio transport.

This exposes read-only tools for querying Fabrica state: version, doctor,
status, drift, cost-report, and config-show.

To connect an MCP client, run this command as a subprocess. The server
communicates over stdin/stdout using newline-delimited JSON.

Example:
  fabrica mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			server := NewServer(rt)
			return server.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}

// NewServer creates an MCP server with all Fabrica tools registered.
func NewServer(rt globals.Runtime) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "fabrica",
		Version: fabricav.String(),
	}, nil)
	registerTools(s, rt)
	return s
}
