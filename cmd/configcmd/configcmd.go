package configcmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type showCommand struct {
	cfg *config.Config
	out io.Writer
}

var Cmd = &cobra.Command{
	Use:   "config",
	Short: "View and modify fabrica.yaml configuration",
}

func init() {
	Cmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		rt := globals.Current()
		if rt.Config == nil {
			return fmt.Errorf("no configuration loaded")
		}
		return showCommand{cfg: rt.Config, out: os.Stdout}.run()
	},
}

func (c showCommand) run() error {
	out, err := c.cfg.YAML()
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
	backend := fabricastate.ResolveBackendNames(c.cfg, c.cfg.Cloud.AWS.AccountID)
	fmt.Fprintln(c.out, "Resolved resource names:")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  S3 bucket:      %s\n", backend.Bucket)
	fmt.Fprintf(c.out, "  DynamoDB table: %s\n", backend.Table)
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
}
