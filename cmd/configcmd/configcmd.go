package configcmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "config",
	Short: "View and modify fabrica.yaml configuration",
}

func init() {
	Cmd.AddCommand(showCmd)
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		if globals.Cfg == nil {
			return fmt.Errorf("no configuration loaded")
		}
		printConfig()
		return nil
	},
}

func printConfig() {
	cfg := globals.Cfg

	fmt.Println("cloud:")
	fmt.Printf("  provider: %s\n", cfg.Cloud.Provider)
	fmt.Println("  aws:")
	fmt.Printf("    region: %s\n", cfg.Cloud.AWS.Region)
	if cfg.Cloud.AWS.Profile != "" {
		fmt.Printf("    profile: %s\n", cfg.Cloud.AWS.Profile)
	}
	if cfg.Cloud.AWS.AccountID != "" {
		cfgTag := cfg.Cloud.AWS.AccountID
		fmt.Printf("    accountId: %s\n", cfgTag)
	}
	if len(cfg.Cloud.AWS.Tags) > 0 {
		fmt.Println("    tags:")
		keys := make([]string, 0, len(cfg.Cloud.AWS.Tags))
		for k := range cfg.Cloud.AWS.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("      %s: %s\n", k, cfg.Cloud.AWS.Tags[k])
		}
	}
	fmt.Println()
	fmt.Println("state:")
	if cfg.State.Bucket != "" {
		fmt.Printf("  bucket: %s\n", cfg.State.Bucket)
	}
	fmt.Printf("  table: %s\n", cfg.State.Table)
	if cfg.State.KMSKeyID != "" {
		fmt.Printf("  kmsKeyId: %s\n", cfg.State.KMSKeyID)
	}
	fmt.Println()

	// Print module sections even if empty
	fmt.Println("perforce: {}")
	fmt.Println("horde: {}")
	fmt.Println("ci: {}")
	fmt.Println("cost: {}")
	fmt.Println()

	// Show resolved resource names
	bucket := cfg.State.Bucket
	if bucket == "" {
		bucket = "fabrica-state-<account-id>"
	}
	fmt.Println("Resolved resource names:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  S3 bucket:      %s\n", bucket)
	fmt.Printf("  DynamoDB table: %s\n", cfg.State.Table)
	fmt.Println(strings.Repeat("-", 50))
}
