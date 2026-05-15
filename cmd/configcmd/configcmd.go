package configcmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type showCommand struct {
	runtime globals.Runtime
	out     io.Writer
}

func New(runtimeSource globals.RuntimeSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and modify fabrica.yaml configuration",
	}
	cmd.AddCommand(newShowCommand(runtimeSource, out))
	return cmd
}

func newShowCommand(runtimeSource globals.RuntimeSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			return showCommand{runtime: rt, out: out}.run()
		},
	}
}

func (c showCommand) run() error {
	out, err := c.runtime.Config.YAML()
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, string(out))
	if len(out) == 0 || out[len(out)-1] != '\n' {
		fmt.Fprintln(c.out)
	}

	c.printResolvedNames()
	return nil
}

func (c showCommand) printResolvedNames() {
	backend := fabricastate.ResolveBackendNames(c.runtime.Config, c.runtime.Config.Cloud.AWS.AccountID)
	fmt.Fprintln(c.out, "Resolved resource names:")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  S3 bucket:      %s\n", backend.Bucket)
	fmt.Fprintf(c.out, "  DynamoDB table: %s\n", backend.Table)
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
}
