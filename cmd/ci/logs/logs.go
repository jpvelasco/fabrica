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
		Long: `Fetch the CloudWatch log output for a CodeBuild build and print it to stdout.

Get the build ID from 'fabrica ci trigger' output or 'fabrica ci status'. Logs
appear once the build reaches its build phase; very early or queued builds may
have none yet.`,
		Example: `  # Fetch logs for a build (id from 'ci trigger' or 'ci status'):
  fabrica ci logs fabrica-ci:1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d

  # Save logs to a file:
  fabrica ci logs fabrica-ci:1a2b3c4d-... > build.log`,
		Args: cobra.ExactArgs(1),
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
		return fmt.Errorf("no CodeBuild-capable cloud provider configured — check your credentials and that cloud.provider is \"aws\"")
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
