package setup

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "setup",
	Short: "Provision the state backend",
	Long: `Set up the Fabrica state backend (S3 bucket + DynamoDB lock table).

This command detects your AWS account and creates the infrastructure
required for Fabrica to manage state. With --dry-run, it shows what
would be created and the estimated monthly cost.`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	if globals.Provider == nil {
		return fmt.Errorf("no cloud provider loaded — check your config")
	}

	account, _, region, err := globals.Provider.Identity(cmd.Context())
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity — check your credentials: %w", err)
	}

	cfg := globals.Cfg
	plan := fabricastate.NewSetupPlan(cfg, account, region)
	tags := setupTags(cfg.Cloud.AWS.Tags)

	if globals.DryRun {
		return runDryRun(plan, tags)
	}

	return runReal(cmd.Context(), plan)
}

func setupTags(extra map[string]string) map[string]string {
	tags := fabricatags.Standard("setup", fabricav.Version)
	for k, v := range extra {
		tags[k] = v
	}
	return tags
}

func runDryRun(plan fabricastate.SetupPlan, tags map[string]string) error {
	fmt.Println("Setup (dry run)")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  Account:  %s\n", plan.Account)
	fmt.Printf("  Region:   %s\n", plan.Region)
	fmt.Println()

	fmt.Println("Resources to create:")
	for _, r := range plan.Resources {
		fmt.Printf("  %-24s %s\n", r.Kind+":", r.Identifier)
	}
	fmt.Println()

	fmt.Println("Tags:")
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-20s %s\n", k+":", tags[k])
	}
	fmt.Println()

	fmt.Println("Cost estimate:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  %-28s %10s  %s\n", "Resource", "Cost/mo", "Confidence")
	fmt.Println(strings.Repeat("-", 50))

	report := fabricacost.Global.EstimateAll(costResources(plan.Resources))
	for _, result := range report.Results {
		if result.Err != nil {
			fmt.Printf("  %-28s %10s  %s\n", result.Resource.Name, "-", "(estimator not registered)")
			continue
		}
		fmt.Printf("  %-28s  $%-8.2f  %s\n", result.Resource.Name, result.Monthly.Amount, result.Monthly.Confidence)
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  %-28s  $%-8.2f\n", "Total:", report.Total)
	fmt.Println()
	fmt.Printf("Confidence: %s\n", report.Confidence)
	fmt.Println()
	fmt.Println("Run without --dry-run to proceed.")
	return nil
}

func costResources(resources []fabricastate.ResourcePlan) []fabricacost.Resource {
	out := make([]fabricacost.Resource, 0, len(resources))
	for _, r := range resources {
		out = append(out, fabricacost.Resource{
			TypeName: r.TypeName,
			Name:     fmt.Sprintf("%s (%s)", r.Kind, r.Identifier),
		})
	}
	return out
}

func runReal(ctx context.Context, plan fabricastate.SetupPlan) error {
	fmt.Println("Setting up state backend...")
	fmt.Println()
	fmt.Printf("  Account:  %s\n", plan.Account)
	fmt.Printf("  Region:   %s\n", plan.Region)
	fmt.Printf("  Bucket:   %s\n", plan.Backend.Bucket)
	fmt.Printf("  Table:    %s\n", plan.Backend.Table)
	fmt.Println()

	// Bootstrap returns results
	results, err := fabricastate.Bootstrap(ctx, globals.Provider, globals.Cfg)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	allExisted := true
	for _, r := range results {
		fmt.Println("  " + strings.TrimSpace(r.String()))
		if !r.Existed {
			allExisted = false
		}
	}
	fmt.Println()

	// Write account ID into config
	if globals.Cfg.Cloud.AWS.AccountID == "" {
		globals.Cfg.Cloud.AWS.AccountID = plan.Account
		if err := globals.Cfg.Save(globals.ConfigPath); err != nil {
			fmt.Printf("Warning: could not save config to %s: %v\n", globals.ConfigFile(), err)
			fmt.Println("Please update your config with account ID: " + plan.Account)
		}
	}

	if allExisted {
		fmt.Println("All resources already exist. Nothing changed.")
	} else {
		fmt.Println("Setup complete.")
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  fabrica doctor               Verify environment health")
	fmt.Println("  fabrica config show          Inspect configuration")
	fmt.Println("  fabrica version              Show version information")

	return nil
}
