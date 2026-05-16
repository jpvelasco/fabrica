package destroy

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type command struct {
	runtime   globals.Runtime
	all       bool
	dryRun    bool
	assumeYes bool
	out       io.Writer
	confirm   func(string) bool
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down provisioned infrastructure",
		Long: `Safely dismantle Fabrica-managed infrastructure.

By default, this command shows a summary of what would be destroyed
if --all is provided. It never mutates infrastructure without explicit
confirmation.

Use --all to target all provisioned resources. The command will walk
through a confirmation dialog before proceeding.

Run with --all --yes to skip the interactive prompt (use with care).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			return command{
				runtime:   rt,
				all:       all,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				out:       out,
				confirm:   prompt.Confirm,
			}.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include all provisioned infrastructure")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if !c.all {
		c.printUsageHint()
		return nil
	}

	if c.runtime.Provider == nil {
		fmt.Fprintln(c.out, "No infrastructure found. Nothing to destroy.")
		return nil
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	backend := fabricastate.ResolveBackendNames(c.runtime.Config, account)
	c.printPlan(account, region, backend)

	if c.dryRun {
		c.printDryRun()
		return nil
	}

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		if !c.confirm("Continue with destroy?") {
			fmt.Fprintln(c.out, "Aborted.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding (--yes flag set).")
	}

	destroyer, ok := c.runtime.Provider.(cloud.StateBackendDestroyer)
	if !ok {
		return fmt.Errorf("provider %s does not support state backend destroy", c.runtime.Provider.Name())
	}
	return c.destroyBackend(ctx, destroyer, backend)
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

func (c command) printDryRun() {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Dry run: no resources will be deleted.")
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) destroyBackend(ctx context.Context, destroyer cloud.StateBackendDestroyer, backend fabricastate.BackendNames) error {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Destroying state backend...")
	fmt.Fprintln(c.out)

	bucket, err := destroyer.DeleteStateBucket(ctx, backend.Bucket)
	if err != nil {
		return err
	}
	c.printDeleteResult("S3 bucket", bucket)

	table, err := destroyer.DeleteStateLockTable(ctx, backend.Table)
	if err != nil {
		return err
	}
	c.printDeleteResult("DynamoDB lock table", table)

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Destroy complete.")
	return nil
}

func (c command) printDeleteResult(label string, result cloud.StateBackendDeleteResult) {
	switch {
	case result.Deleted:
		fmt.Fprintf(c.out, "  deleted %s %s\n", label, result.Identifier)
	case result.Missing:
		fmt.Fprintf(c.out, "  %s %s not found; skipping\n", label, result.Identifier)
	default:
		fmt.Fprintf(c.out, "  %s %s unchanged\n", label, result.Identifier)
	}
}
