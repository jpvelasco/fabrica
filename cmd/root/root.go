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
	Short:        "Provision game studio cloud infrastructure on AWS",
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&globals.Verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&globals.JSONOutput, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&globals.DryRun, "dry-run", false, "print plan without executing")
	rootCmd.PersistentFlags().BoolVar(&globals.AssumeYes, "yes", false, "assume yes to all prompts")
	rootCmd.PersistentFlags().StringVar(&globals.Profile, "profile", "", "use FABRICA_PROFILE (e.g., 'staging')")
}
