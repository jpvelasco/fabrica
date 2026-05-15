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
	fmt.Println("  provider: " + cfg.Cloud.Provider)
	fmt.Println("  aws:")
	fmt.Println("    region: " + cfg.Cloud.AWS.Region)
	if cfg.Cloud.AWS.Profile != "" {
		fmt.Println("    profile: " + cfg.Cloud.AWS.Profile)
	}
	if cfg.Cloud.AWS.AccountID != "" {
		fmt.Println("    accountId: " + cfg.Cloud.AWS.AccountID)
	}
	if len(cfg.Cloud.AWS.Tags) > 0 {
		fmt.Println("    tags:")
		keys := make([]string, 0, len(cfg.Cloud.AWS.Tags))
		for k := range cfg.Cloud.AWS.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Println("      " + k + ": " + cfg.Cloud.AWS.Tags[k])
		}
	}
	fmt.Println()
	fmt.Println("state:")
	if cfg.State.Bucket != "" {
		fmt.Println("  bucket: " + cfg.State.Bucket)
	}
	fmt.Println("  table: " + cfg.State.Table)
	if cfg.State.KMSKeyID != "" {
		fmt.Println("  kmsKeyId: " + cfg.State.KMSKeyID)
	}
	fmt.Println()

	// Print module sections even if empty
	fmt.Println("perforce: {}")
	fmt.Println("horde: {}")
	fmt.Println("ci: {}")
	fmt.Println("cost: {}")
	fmt.Println()

	// Print resolved info
	if len(cfg.Cloud.AWS.Tags) > 0 {
		fmt.Println("# Standard tags will be applied:")
		for k, v := range cfg.Cloud.AWS.Tags {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	fmt.Println()
	fmt.Println("# Resolved resource names:")
	fmt.Println(strings.Repeat("-", 50))
	bucket := cfg.State.Bucket
	if bucket == "" {
		fmt.Printf("  S3 bucket:      fabrica-state-(account id after setup)\n")
	} else {
		fmt.Printf("  S3 bucket:      %s\n", bucket)
	}
	fmt.Printf("  DynamoDB table: %s\n", cfg.State.Table)
	fmt.Println(strings.Repeat("-", 50))
}
