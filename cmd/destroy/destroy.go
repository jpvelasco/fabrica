package destroy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const destroyLineWidth = 62

type command struct {
	runtime   globals.Runtime
	all       bool
	dryRun    bool
	assumeYes bool
	out       io.Writer
	confirm   func(string, string) bool
}

type destroyPlan struct {
	Account string
	Region  string
	Backend fabricastate.BackendNames
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
				confirm:   prompt.ConfirmExact,
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

	plan := destroyPlan{
		Account: account,
		Region:  region,
		Backend: fabricastate.ResolveBackendNames(c.runtime.Config, account),
	}

	if c.dryRun {
		c.printDryRunPlan(plan)
		return nil
	}

	c.printDestroyPlan(plan)

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		c.printConfirmationInstructions(plan)
		if !c.confirm("Enter confirmation phrase", c.confirmationPhrase(plan)) {
			fmt.Fprintln(c.out, "Destroy cancelled: confirmation phrase did not match.")
			fmt.Fprintln(c.out, "No AWS delete calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
		fmt.Fprintln(c.out, "Use --yes only in automation you trust.")
	}

	destroyer, ok := c.runtime.Provider.(cloud.StateBackendDestroyer)
	if !ok {
		return fmt.Errorf("provider %s does not support state backend destroy", c.runtime.Provider.Name())
	}
	return c.destroyBackend(ctx, destroyer, plan)
}

func (c command) printUsageHint() {
	fmt.Fprintln(c.out, "To destroy infrastructure, use --all:")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  fabrica destroy --all")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "This requires explicit confirmation. Use --all --yes to skip the prompt.")
}

func (c command) printDryRunPlan(plan destroyPlan) {
	fmt.Fprintln(c.out, "Destroy dry run")
	fmt.Fprintln(c.out, strings.Repeat("-", destroyLineWidth))
	fmt.Fprintln(c.out, "No resources will be deleted. No AWS delete calls will be made.")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources that would be deleted:")
	c.printResourceList(plan)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Deletion order if run for real:")
	fmt.Fprintln(c.out, "  1. S3 state bucket")
	fmt.Fprintln(c.out, "  2. DynamoDB lock table")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run without --dry-run only after verifying the account, region, and names above.")
}

func (c command) printDestroyPlan(plan destroyPlan) {
	fmt.Fprintln(c.out, "Destroy plan")
	fmt.Fprintln(c.out, strings.Repeat("-", destroyLineWidth))
	fmt.Fprintln(c.out, "This command is about to permanently delete the Fabrica Phase 0 state backend.")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to delete:")
	c.printResourceList(plan)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Deletion order:")
	fmt.Fprintln(c.out, "  1. S3 state bucket")
	fmt.Fprintln(c.out, "  2. DynamoDB lock table")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "IRREVERSIBLE: Deleted state backend resources cannot be recovered by Fabrica.")
	fmt.Fprintln(c.out, "SAFETY: The S3 bucket must already be empty. This command will not delete bucket objects, object versions, or delete markers.")
	fmt.Fprintln(c.out, "FAILURE POLICY: If S3 bucket deletion fails, DynamoDB table deletion will not be attempted.")
}

func (c command) printPlanDetails(plan destroyPlan) {
	fmt.Fprintf(c.out, "  AWS account ID: %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:     %s\n", plan.Region)
}

func (c command) printResourceList(plan destroyPlan) {
	fmt.Fprintf(c.out, "  S3 state bucket:      %s\n", plan.Backend.Bucket)
	fmt.Fprintf(c.out, "  DynamoDB lock table:  %s\n", plan.Backend.Table)
}

func (c command) confirmationPhrase(plan destroyPlan) string {
	return fmt.Sprintf("destroy %s %s", plan.Account, plan.Backend.Bucket)
}

func (c command) printConfirmationInstructions(plan destroyPlan) {
	fmt.Fprintln(c.out, "Final confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", c.confirmationPhrase(plan))
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels destroy.")
}

func (c command) destroyBackend(ctx context.Context, destroyer cloud.StateBackendDestroyer, plan destroyPlan) error {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Destroying state backend...")
	fmt.Fprintln(c.out)

	bucket, err := destroyer.DeleteStateBucket(ctx, plan.Backend.Bucket)
	if err != nil {
		c.printDeleteFailure("S3 state bucket", plan.Backend.Bucket, err)
		return fmt.Errorf("destroy failed before deleting DynamoDB lock table: %w", err)
	}
	c.printDeleteResult("S3 state bucket", bucket)

	table, err := destroyer.DeleteStateLockTable(ctx, plan.Backend.Table)
	if err != nil {
		c.printDeleteFailure("DynamoDB lock table", plan.Backend.Table, err)
		return fmt.Errorf("destroy partially completed; S3 bucket handled but DynamoDB table deletion failed: %w", err)
	}
	c.printDeleteResult("DynamoDB lock table", table)

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Destroy complete.")
	return nil
}

func (c command) printDeleteResult(label string, result cloud.StateBackendDeleteResult) {
	switch {
	case result.Deleted:
		fmt.Fprintf(c.out, "  deleted %s: %s\n", label, result.Identifier)
	case result.Missing:
		fmt.Fprintf(c.out, "  %s not found; skipping: %s\n", label, result.Identifier)
	default:
		fmt.Fprintf(c.out, "  %s unchanged: %s\n", label, result.Identifier)
	}
}

func (c command) printDeleteFailure(label, identifier string, err error) {
	fmt.Fprintf(c.out, "  failed to delete %s: %s\n", label, identifier)
	if errors.Is(err, cloud.ErrStateBucketNotEmpty) {
		fmt.Fprintln(c.out, "  The bucket is not empty. Empty all objects, object versions, and delete markers, then run destroy again.")
		fmt.Fprintln(c.out, "  DynamoDB lock table deletion was not attempted.")
	} else {
		fmt.Fprintf(c.out, "  Error: %v\n", err)
	}
}
