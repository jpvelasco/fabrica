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

	// Resolve identity
	account, _, region, err := globals.Provider.Identity(cmd.Context())
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity — check your credentials: %w", err)
	}

	cfg := globals.Cfg

	// Determine resource names
	bucket := cfg.State.Bucket
	if bucket == "" {
		bucket = "fabrica-state-" + account
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
		return runDryRun(account, region, bucket, table, tags)
	}

	return runReal(cmd.Context(), account, region, bucket, table)
}

func runDryRun(account, region, bucket, table string, tags map[string]string) error {
	fmt.Println("Setup (dry run)")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  Account:  %s\n", account)
	fmt.Printf("  Region:   %s\n", region)
	fmt.Println()

	fmt.Println("Resources to create:")
	fmt.Printf("  %-24s %s\n", "S3 bucket:", bucket)
	fmt.Printf("  %-24s %s\n", "DynamoDB table:", table)
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

	var total float64
	type costEntry struct {
		label    string
		typeName string
		monthly  fabricacost.Monthly
	}
	entries := []costEntry{
		{label: "S3 bucket (" + bucket + ")", typeName: "AWS::S3::Bucket"},
		{label: "DynamoDB table (" + table + ")", typeName: "AWS::DynamoDB::Table"},
	}

	for _, e := range entries {
		m, err := fabricacost.Global.Estimate(e.typeName, fabricacost.Resource{TypeName: e.typeName})
		if err != nil {
			fmt.Printf("  %-28s %10s  %s\n", e.label, "—", "(estimator not registered)")
			continue
		}
		e.monthly = m
		total += m.Amount
		fmt.Printf("  %-28s  $%-8.2f  %s\n", e.label, m.Amount, m.Confidence)
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  %-28s  $%-8.2f\n", "Total:", total)
	fmt.Println()

	// Overall confidence: lowest confidence of all entries
	confidence := fabricacost.High
	for _, e := range entries {
		if e.monthly.Confidence < confidence {
			confidence = e.monthly.Confidence
		}
	}
	fmt.Printf("Confidence: %s\n", confidence)
	fmt.Println()
	fmt.Println("Run without --dry-run to proceed.")
	return nil
}

func runReal(ctx context.Context, account, region, bucket, table string) error {
	fmt.Println("Setting up state backend...")
	fmt.Println()
	fmt.Printf("  Account:  %s\n", account)
	fmt.Printf("  Region:   %s\n", region)
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
	fmt.Println("  fabrica config show          Inspect configuration")
	fmt.Println("  fabrica version              Show version information")

	return nil
}
