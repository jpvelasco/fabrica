package configcmd

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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
	out, err := cfg.YAML()
	if err != nil {
		fmt.Printf("error rendering config: %v\n", err)
		return
	}
	fmt.Print(string(out))
	if len(out) == 0 || out[len(out)-1] != '\n' {
		fmt.Println()
	}

	// Show resolved resource names
	backend := fabricastate.ResolveBackendNames(cfg, cfg.Cloud.AWS.AccountID)
	fmt.Println("Resolved resource names:")
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("  S3 bucket:      %s\n", backend.Bucket)
	fmt.Printf("  DynamoDB table: %s\n", backend.Table)
	fmt.Println(strings.Repeat("-", 50))
}
