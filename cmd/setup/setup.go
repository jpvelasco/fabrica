package setup

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

type command struct {
	runtime      globals.Runtime
	dryRun       bool
	assumeYes    bool
	out          io.Writer
	costs        *fabricacost.Registry
	version      string
	applyBackend func(context.Context, fabricac.Provider, fabricastate.BackendSetupApplyPlan) ([]fabricastate.BootstrapResult, error)
	confirm      func(prompt string) bool
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision the state backend",
		Long: `Set up the Fabrica state backend (S3 bucket + DynamoDB lock table).

Creates the S3 state bucket (with versioning, encryption, and a public
access block) and the DynamoDB lock table for this AWS account. The
operation is idempotent: existing resources are left in place and
re-running reconciles their configuration.

You are asked to confirm before any resources are created; pass --yes to
skip the prompt. With --dry-run, it shows the planned resources and the
estimated monthly cost without creating anything.`,
		Example: `  # Preview what would be created, with a cost estimate (no changes):
  fabrica setup --dry-run

  # Create the state backend, confirming interactively:
  fabrica setup

  # Create it non-interactively (CI / automation):
  fabrica setup --yes

  # Target a specific account profile and config:
  fabrica setup --profile studio --config ./fabrica.studio.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			return command{
				runtime:      rt,
				dryRun:       opts.DryRun,
				assumeYes:    opts.AssumeYes,
				out:          out,
				costs:        fabricacost.Global,
				version:      fabricav.Version,
				applyBackend: fabricastate.ApplyBackendSetup,
				confirm:      prompt.Confirm,
			}.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	identity, err := c.runtime.Config.ResolveIdentity()
	if err != nil {
		return err
	}
	effective, target, err := provision.PrepareSetupRuntime(ctx, c.runtime)
	if err != nil {
		c.printIdentityHelp()
		return err
	}
	ownership, err := inspectSetup(ctx, effective, target, identity)
	if err != nil {
		return err
	}
	plan, err := fabricastate.PlanBackendSetup(target, identity, ownership, c.version, effective.Config.Cloud.AWS.Tags)
	if err != nil {
		return err
	}
	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}
	return c.runApply(ctx, effective, plan)
}

// inspectSetup validates local state only after confirmed canonical absence.
// Local inventory is never passed to the backend ownership classifier.
func inspectSetup(ctx context.Context, rt globals.Runtime, target fabricastate.SetupPlan, identity fabricatags.Identity) (fabricastate.BackendOwnershipPlan, error) {
	evidence, ownership, err := fabricastate.InspectBackendOwnership(ctx, rt.Provider, target, identity)
	if err != nil {
		return fabricastate.BackendOwnershipPlan{}, err
	}
	if evidence.Canonical == nil {
		if _, err := provision.ReadLocalState(provision.StatePath(rt), target.Account, target.Region, identity); err != nil {
			return fabricastate.BackendOwnershipPlan{}, fmt.Errorf("validating local setup cache: %w", err)
		}
	}
	return ownership, nil
}

// printIdentityHelp lists the common causes of an identity failure so the user
// can self-diagnose before reading the wrapped SDK error.
func (c command) printIdentityHelp() {
	fmt.Fprintln(c.out, "Could not authenticate with AWS. Common causes:")
	fmt.Fprintln(c.out, "  - Missing or expired credentials — refresh them (e.g. 'aws sso login') and retry.")
	fmt.Fprintln(c.out, "  - Wrong profile — set 'cloud.aws.profile' in fabrica.yaml or pass --profile.")
	fmt.Fprintln(c.out, "  - Region not set or unreachable — set 'cloud.aws.region' in fabrica.yaml.")
	fmt.Fprintln(c.out, "Verify with 'aws sts get-caller-identity', then run 'fabrica doctor'.")
	fmt.Fprintln(c.out)
}

func (c command) printDryRun(plan fabricastate.BackendSetupApplyPlan) {
	fmt.Fprintln(c.out, "Setup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  Account:  %s\n", plan.Target.Account)
	fmt.Fprintf(c.out, "  Region:   %s\n", plan.Target.Region)
	fmt.Fprintln(c.out)
	c.printBackendPlan(plan)
	c.printCostReport(c.costs.EstimateAll(costResources(plan.Target.Resources)))
	fmt.Fprintln(c.out, "Run without --dry-run to apply this plan.")
}

func (c command) printBackendPlan(plan fabricastate.BackendSetupApplyPlan) {
	actions := map[fabricastate.BackendComponentAction]string{
		fabricastate.BackendComponentCreate:       "Create",
		fabricastate.BackendComponentReconcile:    "Reconcile",
		fabricastate.BackendComponentRepairLegacy: "Repair legacy ownership",
	}
	for _, component := range []struct {
		name   string
		action fabricastate.BackendComponentAction
		tags   map[string]string
	}{{plan.Target.Backend.Bucket, plan.Ownership.Bucket.Action, plan.BucketTags},
		{plan.Target.Backend.Table, plan.Ownership.Table.Action, plan.TableTags}} {
		fmt.Fprintf(c.out, "  %s %s\n", actions[component.action], component.name)
		c.printTags(component.tags)
	}
}

func (c command) printTags(tags map[string]string) {
	fmt.Fprintln(c.out, "Tags:")
	for _, k := range sortedKeys(tags) {
		fmt.Fprintf(c.out, "  %-20s %s\n", k+":", tags[k])
	}
	fmt.Fprintln(c.out)
}

func (c command) printCostReport(report fabricacost.Report) {
	fmt.Fprintln(c.out, "Cost estimate:")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  %-28s %10s  %s\n", "Resource", "Cost/mo", "Confidence")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	for _, result := range report.Results {
		if result.Err != nil {
			fmt.Fprintf(c.out, "  %-28s %10s  %s\n", result.Resource.Name, "-", "(estimator not registered)")
			continue
		}
		fmt.Fprintf(c.out, "  %-28s  $%-8.2f  %s\n", result.Resource.Name, result.Monthly.Amount, result.Monthly.Confidence)
	}

	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  %-28s  $%-8.2f\n", "Total:", report.Total)
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Confidence: %s\n", report.Confidence)
	fmt.Fprintln(c.out)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func costResources(resources []fabricastate.ResourcePlan) []fabricacost.Resource {
	out := make([]fabricacost.Resource, 0, len(resources))
	for _, r := range resources {
		out = append(out, fabricacost.Resource{
			TypeName: r.TypeName,
			Name:     fmt.Sprintf("%s (%s)", r.Label, r.Identifier),
		})
	}
	return out
}

func (c command) runApply(ctx context.Context, effective globals.Runtime, plan fabricastate.BackendSetupApplyPlan) error {
	c.printApplyHeader(plan.Target)
	c.printBackendPlan(plan)
	if !provision.ConfirmSetup(c.out, "Apply the S3 bucket and DynamoDB table plan shown above?", c.assumeYes, c.confirm) {
		return nil
	}
	if c.assumeYes {
		fmt.Fprintln(c.out)
	}
	results, err := c.applyConfirmed(ctx, effective, plan)
	if err != nil {
		c.printBootstrapResults(results)
		c.printRecovery(plan.Target)
		return fmt.Errorf("setup failed: %w", err)
	}
	return nil
}

func (c command) applyConfirmed(ctx context.Context, effective globals.Runtime, plan fabricastate.BackendSetupApplyPlan) ([]fabricastate.BootstrapResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := inspectSetup(ctx, effective, plan.Target, plan.Identity); err != nil {
		return nil, err
	}
	_, requiresLock := effective.Provider.(fabricac.StateLockManager)
	if plan.Ownership.Table.Observed.Exists {
		acquired, release, err := provision.AcquireStateLock(ctx, effective, "setup")
		if err != nil {
			return nil, err
		}
		defer release()
		ctx = acquired
		if requiresLock && !provision.StateLockHeld(ctx) {
			return nil, fmt.Errorf("setup lock was not acquired; verify the existing lock table and repeat setup")
		}
	}
	results, err := c.applyBackend(ctx, effective.Provider, plan)
	if err != nil {
		return results, err
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}
	if !provision.StateLockHeld(ctx) {
		acquired, release, err := provision.AcquireStateLock(ctx, effective, "setup")
		if err != nil {
			return results, fmt.Errorf("acquiring setup lock after backend apply: %w", err)
		}
		defer release()
		ctx = acquired
	}
	if requiresLock && !provision.StateLockHeld(ctx) {
		return results, fmt.Errorf("setup completion has no live state lock; verify the created lock table and repeat setup")
	}
	final, err := inspectSetup(ctx, effective, plan.Target, plan.Identity)
	if err != nil {
		return results, fmt.Errorf("checking setup completion: %w", err)
	}
	if final.Bucket.Action != fabricastate.BackendComponentReconcile || final.Table.Action != fabricastate.BackendComponentReconcile {
		return results, fmt.Errorf("setup completion changed backend actions; inspect both components and repeat the full preflight")
	}
	if err = ctx.Err(); err == nil {
		c.printBootstrapResults(results)
		c.saveBackendConfig(plan.Target)
		c.printCompletion(plan.Target, results)
	}
	return results, err
}

// printRecovery explains how to recover from a partial bootstrap failure.
// Compatible components may be reconciled on retry; ambiguous ownership needs
// inspection before another apply, including an untagged crash-created bucket.
func (c command) printRecovery(plan fabricastate.SetupPlan) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Setup did not finish — compatible resources are safe to recover:")
	fmt.Fprintln(c.out, "  - setup is idempotent: fix the error above, then run 'fabrica setup' again.")
	fmt.Fprintln(c.out, "    Inspect existing components; only compatible ownership can be reconciled.")
	fmt.Fprintln(c.out, "  - run 'fabrica doctor' to see which resources already exist.")
	fmt.Fprintf(c.out, "  - expected resources: S3 bucket %s, DynamoDB table %s.\n", plan.Backend.Bucket, plan.Backend.Table)
}

func (c command) printApplyHeader(plan fabricastate.SetupPlan) {
	fmt.Fprintln(c.out, "Setting up state backend...")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Account:  %s\n", plan.Account)
	fmt.Fprintf(c.out, "  Region:   %s\n", plan.Region)
	fmt.Fprintf(c.out, "  Bucket:   %s\n", plan.Backend.Bucket)
	fmt.Fprintf(c.out, "  Table:    %s\n", plan.Backend.Table)
	fmt.Fprintln(c.out)
}

func (c command) printBootstrapResults(results []fabricastate.BootstrapResult) {
	for _, r := range results {
		fmt.Fprintln(c.out, "  "+strings.TrimSpace(r.String()))
	}
	fmt.Fprintln(c.out)
}

func (c command) saveBackendConfig(plan fabricastate.SetupPlan) {
	updates := map[string]any{}
	if c.runtime.Config.Cloud.AWS.AccountID == "" {
		c.runtime.Config.Cloud.AWS.AccountID = plan.Account
		updates["cloud.aws.accountId"] = plan.Account
	}
	if c.runtime.Config.State.Bucket == "" {
		c.runtime.Config.State.Bucket = plan.Backend.Bucket
		updates["state.bucket"] = plan.Backend.Bucket
	}
	if c.runtime.Config.State.Table == "" {
		c.runtime.Config.State.Table = plan.Backend.Table
		updates["state.table"] = plan.Backend.Table
	}
	if len(updates) == 0 {
		return
	}
	if err := c.runtime.Config.SaveKeys(c.runtime.ConfigPath, updates); err != nil {
		fmt.Fprintf(c.out, "Warning: could not save config to %s: %v\n", c.runtime.ConfigFile(), err)
		fmt.Fprintln(c.out, "Please update your config with account ID: "+plan.Account)
	}
}

func (c command) printCompletion(plan fabricastate.SetupPlan, results []fabricastate.BootstrapResult) {
	allExisted := allResourcesExisted(results)
	if allExisted {
		fmt.Fprintln(c.out, "State backend verified and configured.")
	} else {
		fmt.Fprintln(c.out, "Setup complete — your state backend is ready.")
	}
	fmt.Fprintln(c.out)

	// What just happened: name the resources backing remote state.
	verb := "Created"
	if allExisted {
		verb = "Verified"
	}
	fmt.Fprintln(c.out, "What just happened:")
	fmt.Fprintf(c.out, "  %s the S3 bucket %q (versioned, encrypted, public access blocked)\n", verb, plan.Backend.Bucket)
	fmt.Fprintf(c.out, "  %s the DynamoDB table %q (distributed state locking)\n", verb, plan.Backend.Table)
	fmt.Fprintln(c.out, "  Together these store Fabrica's remote state for this account.")
	c.printRunningCost(plan)

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica doctor               Verify environment health")
	fmt.Fprintln(c.out, "  fabrica status               Overview of all modules")
	fmt.Fprintln(c.out, "  fabrica perforce create      Provision Perforce Helix Core")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run 'fabrica status' to see the current state of your studio infrastructure.")
}

// printRunningCost prints the estimated monthly cost of the state backend, so
// the user knows what the just-created resources will cost to keep running.
func (c command) printRunningCost(plan fabricastate.SetupPlan) {
	if c.costs == nil {
		return
	}
	report := c.costs.EstimateAll(costResources(plan.Resources))
	fmt.Fprintf(c.out, "  Estimated cost: ~$%.2f/month (%s confidence)\n", report.Total, report.Confidence)
}

func allResourcesExisted(results []fabricastate.BootstrapResult) bool {
	for _, r := range results {
		if !r.Existed {
			return false
		}
	}
	return true
}
