package setup

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	out       io.Writer
	costs     *fabricacost.Registry
	version   string
	bootstrap func(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error)
	confirm   func(prompt string) bool
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
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				out:       out,
				costs:     fabricacost.Global,
				version:   fabricav.Version,
				bootstrap: fabricastate.Bootstrap,
				confirm:   prompt.Confirm,
			}.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		c.printIdentityHelp()
		return fmt.Errorf("could not resolve AWS identity: %w", err)
	}

	cfg := c.runtime.Config
	plan := fabricastate.NewSetupPlan(cfg, account, region)
	tags := setupTags(c.version, cfg.Cloud.AWS.Tags)

	if c.dryRun {
		c.printDryRun(plan, tags)
		return nil
	}

	return c.runApply(ctx, plan)
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

func setupTags(version string, extra map[string]string) map[string]string {
	tags := fabricatags.Standard("setup", version)
	for k, v := range extra {
		tags[k] = v
	}
	return tags
}

func (c command) printDryRun(plan fabricastate.SetupPlan, tags map[string]string) {
	fmt.Fprintln(c.out, "Setup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", 50))
	fmt.Fprintf(c.out, "  Account:  %s\n", plan.Account)
	fmt.Fprintf(c.out, "  Region:   %s\n", plan.Region)
	fmt.Fprintln(c.out)

	fmt.Fprintln(c.out, "Resources to create:")
	for _, r := range plan.Resources {
		fmt.Fprintf(c.out, "  %-24s %s\n", r.Label+":", r.Identifier)
	}
	fmt.Fprintln(c.out)

	c.printTags(tags)
	c.printCostReport(c.costs.EstimateAll(costResources(plan.Resources)))
	fmt.Fprintln(c.out, "Run without --dry-run to create these resources.")
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

func (c command) runApply(ctx context.Context, plan fabricastate.SetupPlan) error {
	c.printApplyHeader(plan)

	if !c.assumeYes {
		if !c.confirm("Create the S3 bucket and DynamoDB table shown above?") {
			fmt.Fprintln(c.out, "Setup cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
		fmt.Fprintln(c.out)
	}

	results, err := c.bootstrap(ctx, c.runtime.Provider, c.runtime.Config)
	if err != nil {
		c.printRecovery(plan)
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	c.printBootstrapResults(results)
	c.saveBackendConfig(plan)
	c.printCompletion(plan, results)
	return nil
}

// printRecovery explains how to recover from a partial bootstrap failure.
// Setup is idempotent, so the fix is almost always "address the error and
// re-run" — already-created resources are detected and left in place.
func (c command) printRecovery(plan fabricastate.SetupPlan) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Setup did not finish — but this is safe to recover from:")
	fmt.Fprintln(c.out, "  - setup is idempotent: fix the error above, then run 'fabrica setup' again.")
	fmt.Fprintln(c.out, "    Any resource already created is detected and left untouched.")
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
	dirty := false
	if c.runtime.Config.Cloud.AWS.AccountID == "" {
		c.runtime.Config.Cloud.AWS.AccountID = plan.Account
		dirty = true
	}
	if c.runtime.Config.State.Bucket == "" {
		c.runtime.Config.State.Bucket = plan.Backend.Bucket
		dirty = true
	}
	if c.runtime.Config.State.Table == "" {
		c.runtime.Config.State.Table = plan.Backend.Table
		dirty = true
	}
	if !dirty {
		return
	}
	if err := c.runtime.Config.Save(c.runtime.ConfigPath); err != nil {
		fmt.Fprintf(c.out, "Warning: could not save config to %s: %v\n", c.runtime.ConfigFile(), err)
		fmt.Fprintln(c.out, "Please update your config with account ID: "+plan.Account)
	}
}

func (c command) printCompletion(plan fabricastate.SetupPlan, results []fabricastate.BootstrapResult) {
	allExisted := allResourcesExisted(results)
	if allExisted {
		fmt.Fprintln(c.out, "Nothing to do — your state backend is already set up.")
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
	fmt.Fprintf(c.out, "  %s the DynamoDB table %q (state locking)\n", verb, plan.Backend.Table)
	fmt.Fprintln(c.out, "  Together these store and lock Fabrica's remote state for this account.")
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
