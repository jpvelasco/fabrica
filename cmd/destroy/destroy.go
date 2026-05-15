package destroy

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type command struct {
	cfg       *config.Config
	provider  cloud.Provider
	all       bool
	assumeYes bool
	out       io.Writer
}

var Cmd = &cobra.Command{
	Use:   "destroy",
	Short: "Tear down provisioned infrastructure",
	Long: `Safely dismantle Fabrica-managed infrastructure.

By default, this command shows a summary of what would be destroyed
if --all is provided. It never mutates infrastructure without explicit
confirmation.

Use --all to target all provisioned resources. The command will walk
through a confirmation dialog before proceeding.

Run with --all --yes to skip the interactive prompt (use with care).`,
	RunE: runDestroy,
}

var allFlag bool

func init() {
	Cmd.Flags().BoolVarP(&allFlag, "all", "a", false, "Include all provisioned infrastructure")
}

func runDestroy(cmd *cobra.Command, args []string) error {
	rt := globals.Current()
	return command{
		cfg:       rt.Config,
		provider:  rt.Provider,
		all:       allFlag,
		assumeYes: globals.AssumeYes,
		out:       os.Stdout,
	}.run(cmd.Context())
}

func (c command) run(ctx context.Context) error {
	if !c.all {
		c.printUsageHint()
		return nil
	}

	if c.provider == nil {
		fmt.Fprintln(c.out, "No infrastructure found. Nothing to destroy.")
		return nil
	}

	account, _, region, err := c.provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	backend := fabricastate.ResolveBackendNames(c.cfg, account)
	c.printPlan(account, region, backend)

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		if !prompt.Confirm("Continue with destroy?") {
			fmt.Fprintln(c.out, "Aborted.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding (--yes flag set).")
	}

	c.printStub()
	return nil
}

func (c command) printUsageHint() {
	fmt.Fprintln(c.out, "To destroy infrastructure, use --all:")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  fabrica destroy --all")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "This requires explicit confirmation. Use --all --yes to skip the prompt.")
}

func (c command) printPlan(account, region string, backend fabricastate.BackendNames) {
	fmt.Fprintln(c.out, "The following resources will be destroyed:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Account:  %s\n", account)
	fmt.Fprintf(c.out, "  Region:   %s\n", region)
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  S3 bucket:      %s\n", backend.Bucket)
	fmt.Fprintf(c.out, "  DynamoDB table: %s\n", backend.Table)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "This operation cannot be undone.")
}

func (c command) printStub() {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "The destruction logic is not yet implemented. This will be added in Phase 1.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "No resources were destroyed.")
}
