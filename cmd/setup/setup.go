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
		return fmt.Errorf("could not resolve AWS identity — check your credentials and region (try 'aws sts get-caller-identity', or run 'fabrica doctor'): %w", err)
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
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	c.printBootstrapResults(results)
	c.saveAccountID(plan.Account)
	c.printCompletion(results)
	return nil
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

func (c command) saveAccountID(account string) {
	if c.runtime.Config.Cloud.AWS.AccountID != "" {
		return
	}
	c.runtime.Config.Cloud.AWS.AccountID = account
	if err := c.runtime.Config.Save(c.runtime.ConfigPath); err != nil {
		fmt.Fprintf(c.out, "Warning: could not save config to %s: %v\n", c.runtime.ConfigFile(), err)
		fmt.Fprintln(c.out, "Please update your config with account ID: "+account)
	}
}

func (c command) printCompletion(results []fabricastate.BootstrapResult) {
	if allResourcesExisted(results) {
		fmt.Fprintln(c.out, "All resources already exist. Nothing changed.")
	} else {
		fmt.Fprintln(c.out, "Setup complete.")
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica doctor               Verify environment health")
	fmt.Fprintln(c.out, "  fabrica status               Overview of all modules")
	fmt.Fprintln(c.out, "  fabrica perforce create      Provision Perforce Helix Core")
}

func allResourcesExisted(results []fabricastate.BootstrapResult) bool {
	for _, r := range results {
		if !r.Existed {
			return false
		}
	}
	return true
}
