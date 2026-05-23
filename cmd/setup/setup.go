package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	out       io.Writer
	costs     *fabricacost.Registry
	version   string
	bootstrap func(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error)
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision the state backend",
		Long: `Set up the Fabrica state backend (S3 bucket + DynamoDB lock table).

NOTE: Automated provisioning is not yet implemented. This command shows
what would be created and the estimated monthly cost, but does NOT create
any AWS resources. You must create the S3 bucket and DynamoDB table
manually before using other Fabrica commands.

With --dry-run, it shows the planned resources and estimated cost.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			return command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				out:       out,
				costs:     fabricacost.Global,
				version:   fabricav.Version,
				bootstrap: fabricastate.Bootstrap,
			}.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity — check your credentials: %w", err)
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
	fmt.Fprintln(c.out, "NOTE: Automated provisioning is not yet implemented.")
	fmt.Fprintln(c.out, "Resources above must be created manually.")
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

	results, err := c.bootstrap(ctx, c.runtime.Provider, c.runtime.Config)
	if errors.Is(err, fabricastate.ErrBootstrapNotImplemented) {
		c.printNotImplementedWarning()
		return nil
	}
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	c.printBootstrapResults(results)
	c.saveAccountID(plan.Account)
	c.printCompletion(results)
	return nil
}

func (c command) printNotImplementedWarning() {
	fmt.Fprintln(c.out, strings.Repeat("!", 58))
	fmt.Fprintln(c.out, "WARNING: fabrica setup is not yet functional.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "The S3 bucket and DynamoDB table must be created")
	fmt.Fprintln(c.out, "manually before using Fabrica. No AWS resources")
	fmt.Fprintln(c.out, "were created or modified.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Manual setup instructions: docs/setup-manual.md")
	fmt.Fprintln(c.out, strings.Repeat("!", 58))
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
	fmt.Fprintln(c.out, "  fabrica config show          Inspect configuration")
	fmt.Fprintln(c.out, "  fabrica version              Show version information")
}

func allResourcesExisted(results []fabricastate.BootstrapResult) bool {
	for _, r := range results {
		if !r.Existed {
			return false
		}
	}
	return true
}
