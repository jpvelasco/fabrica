// Package export implements "fabrica export": generate infrastructure-as-code
// templates from Fabrica's recorded state and configuration. It reads local
// state (.fabrica/state.json) and config (fabrica.yaml), then produces
// CloudFormation YAML or Terraform HCL for the resources Fabrica manages.
//
// V1 covers the state backend (S3 bucket, DynamoDB table) and the Horde,
// Perforce, and Lore modules. DDC, Workstation, CI, and Deploy are deferred
// to V2.
package export

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricaexport "github.com/jpvelasco/fabrica/internal/export"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type command struct {
	format    string
	output    string
	cfg       *config.Config
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

// New returns the "fabrica export" command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Generate infrastructure-as-code templates from Fabrica state",
		Long: `Generate infrastructure-as-code templates from Fabrica's recorded state and
configuration. Reads local state (.fabrica/state.json) and config (fabrica.yaml),
then produces CloudFormation YAML or Terraform HCL for the resources Fabrica manages.

V1 covers the state backend (S3 bucket, DynamoDB table) and the Horde, Perforce,
and Lore modules. DDC, Workstation, CI, and Deploy are deferred to V2.

No live AWS calls are required — all data comes from local state.`,
		Example: `  # Export as CloudFormation YAML to stdout:
  fabrica export --format cloudformation

  # Export as Terraform HCL to a file:
  fabrica export --format terraform --output infrastructure.tf

  # Preview what would be exported (dry-run):
  fabrica export --format cloudformation --dry-run`,
	}

	var format string
	var output string

	cmd.Flags().StringVar(&format, "format", "", "Export format: cloudformation or terraform (required)")
	cmd.Flags().StringVar(&output, "output", "", "Output file path (default: stdout)")
	_ = cmd.MarkFlagRequired("format") // #nosec G104 -- Cobra flag validation error not actionable at init time

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := runtimeSource()
		if err != nil {
			return err
		}
		c := command{
			format:    format,
			output:    output,
			cfg:       rt.Config,
			out:       out,
			readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		}
		return c.run()
	}

	return cmd
}

func (c *command) run() error {
	// Validate format.
	if !fabricaexport.ValidFormat(c.format) {
		return fmt.Errorf("unsupported export format %q — supported formats: cloudformation, terraform", c.format)
	}

	// Read state.
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	// Generate output.
	data, err := fabricaexport.GenerateOutput(fabricaexport.Format(c.format), st, c.cfg)
	if err != nil {
		if strings.Contains(err.Error(), "no modules to export") {
			fmt.Fprintln(c.out, "Warning: no modules to export — no provisioned modules found in state.")
			return nil
		}
		return err
	}

	// Write output.
	if c.output != "" {
		if err := os.WriteFile(c.output, data, 0600); err != nil {
			return fmt.Errorf("writing output file %s: %w", c.output, err)
		}
		fmt.Fprintf(c.out, "Exported %s template to %s\n", c.format, c.output)
	} else {
		fmt.Fprint(c.out, string(data))
	}

	return nil
}
