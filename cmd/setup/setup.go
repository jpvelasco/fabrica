package setup

import (
	"context"
	"fmt"
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
	Short: "Provision the state backend and bootstrap infrastructure",
	Long: `Set up the Fabrica state backend (S3 bucket + DynamoDB lock table).

In normal mode, this detects your AWS account, estimates costs, and
creates the infrastructure. With --dry-run, it shows what would be
done without making any changes.`,
	RunE: runSetup,
}

func runSetup(cmd *cobra.Command, args []string) error {
	if globals.Provider == nil {
		return fmt.Errorf("no cloud provider loaded -- check your config")
	}

	// Resolve identity
	account, _, region, err := globals.Provider.Identity(cmd.Context())
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	cfg := globals.Cfg

	// Determine resource names
	bucket := cfg.State.Bucket
	if bucket == "" {
		bucket = fmt.Sprintf("fabrica-state-%s", account)
	}
	cfg.State.Bucket = bucket

	table := cfg.State.Table
	if table == "" {
		table = "fabrica-state-lock"
	}
	cfg.State.Table = table

	// Standard tags
	tags := fabricatags.Standard("setup", fabricav.Version)
	for k, v := range cfg.Cloud.AWS.Tags {
		tags[k] = v
	}

	if globals.DryRun {
		return runDryRun(cmd.Context(), account, region, bucket, table, tags)
	}

	return runReal(cmd.Context(), account, region, bucket, table)
}

func runDryRun(ctx context.Context, account, region, bucket, table string, tags map[string]string) error {
	fmt.Println("Dry run — no changes will be made.")
	fmt.Println()
	fmt.Println("Account:    ", account)
	fmt.Println("Region:     ", region)
	fmt.Println()
	fmt.Println("Resource:   ", "S3 bucket", bucket)
	fmt.Println("Resource:   ", "DynamoDB table", table)
	fmt.Println()
	fmt.Print("Tags:\n")
	for k, v := range tags {
		fmt.Printf("    %s\n", fmt.Sprintf("%-17s %s", k+":", v))
	}
	fmt.Println()

	// Cost estimation
	fmt.Println("Cost Estimate:")
	fmt.Println(strings.Repeat("-", 56))
	fmt.Println(fmt.Sprintf("%-30s %10s  %s", "Resource", "$/month", "Confidence"))
	fmt.Println(strings.Repeat("-", 56))

	var total float64
	type costEntry struct {
		label      string
		typeName   string
		monthly    fabricacost.Monthly
	}
	entries := []costEntry{
		{label: "S3 bucket (" + bucket + ")", typeName: "AWS::S3::Bucket"},
		{label: "DynamoDB table (" + table + ")", typeName: "AWS::DynamoDB::Table"},
	}

	for _, e := range entries {
		m, err := fabricacost.Global.Estimate(e.typeName, fabricacost.Resource{TypeName: e.typeName})
		if err != nil {
			// Resource has no registered estimator — skip with note
			fmt.Printf("  %-28s %10s  %s\n", e.label, "—", "(estimator not registered)")
			continue
		}
		e.monthly = m
		total += m.Amount
		fmt.Printf("  %-28s  $%-8.2f  %s\n", e.label, m.Amount, m.Confidence)
	}

	fmt.Println(strings.Repeat("-", 56))
	fmt.Printf("  %-28s  $%-8.2f\n", "Total estimated", total)

	// Overall confidence: lowest confidence of all entries
	confidence := fabricacost.High
	for _, e := range entries {
		if e.monthly.Confidence < confidence {
			confidence = e.monthly.Confidence
		}
	}
	fmt.Printf("  Confidence: %s\n", confidence)

	fmt.Println()
	fmt.Println("Run without --dry-run to proceed.")
	return nil
}

func runReal(ctx context.Context, account, region, bucket, table string) error {
	fmt.Println("Account:", account)
	fmt.Println("Region: ", region)
	fmt.Println()

	// bootstrap returns results
	results, err := fabricastate.Bootstrap(ctx, globals.Provider, globals.Cfg)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	allExisted := true
	for _, r := range results {
		fmt.Println(r.String())
		if !r.Existed {
			allExisted = false
		}
	}
	fmt.Println()

	// Write account ID into config
	if globals.Cfg.Cloud.AWS.AccountID == "" {
		globals.Cfg.Cloud.AWS.AccountID = account
		if err := globals.Cfg.Save("fabrica.yaml"); err != nil {
			fmt.Printf("Warning: could not save config to fabrica.yaml: %v\n", err)
			fmt.Println("Please update fabrica.yaml with account ID: " + account)
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
	fmt.Println("  fabrica status               Inspect provisioned resources (Phase 1)")
	fmt.Println("  fabrica help                 Available commands")

	return nil
}
