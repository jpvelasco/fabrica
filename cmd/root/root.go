package root

import (
	"context"
	"os"
	"os/signal"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "fabrica",
	SilenceUsage: true,
	Short:        "Studio infrastructure provisioning tool",
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&globals.Verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&globals.JSONOutput, "json", "j", false, "Output in JSON format")
	rootCmd.PersistentFlags().BoolVarP(&globals.DryRun, "dry-run", "d", false, "Show what would be done without making changes")
	rootCmd.PersistentFlags().BoolVarP(&globals.AssumeYes, "yes", "y", false, "Assume yes to all prompts")
	rootCmd.PersistentFlags().StringVarP(&globals.Profile, "profile", "p", "", "Configuration profile to use")
}
