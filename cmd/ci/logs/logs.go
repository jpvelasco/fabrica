// Package logs implements "fabrica ci logs <build-id>": fetch CloudWatch logs
// for a specific CodeBuild build.
package logs

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

type command struct {
	runtime globals.Runtime
	buildID string
	out     io.Writer
	runner  cloud.CodeBuildRunner
}

// New returns the "ci logs" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <build-id>",
		Short: "Fetch logs for a specific build",
		Long: `Fetch the CloudWatch log output for a CodeBuild build.

Get the build ID from 'fabrica ci trigger' output or 'fabrica ci status'.`,
		Example: `  fabrica ci logs fabrica-ci:1a2b3c4d-...`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := command{
				runtime: rt,
				buildID: args[0],
				out:     out,
			}
			if rt.Provider != nil {
				if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.runner = r
				}
			}
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c command) run(ctx context.Context) error {
	if c.runner == nil {
		return fmt.Errorf("cloud provider does not support CodeBuild operations")
	}
	log, err := c.runner.BuildLog(ctx, c.buildID)
	if err != nil {
		return fmt.Errorf("fetching logs for build %s: %w", c.buildID, err)
	}
	if log == "" {
		fmt.Fprintf(c.out, "No log output yet for build %s.\n", c.buildID)
		return nil
	}
	fmt.Fprint(c.out, log)
	return nil
}
