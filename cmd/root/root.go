package root

import (
	"context"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

var (
	cfgFile    string
	version    = "dev"
	verbose    bool
	jsonOutput bool
)

var rootCmd = &cobra.Command{
	Use:          "fabrica",
	Version:      version,
	SilenceUsage: true,
	Short:        "Provision game studio cloud infrastructure on AWS",
	Long: `Fabrica provisions and manages the cloud infrastructure a game studio needs
to operate: source control, distributed build farms, CI/CD pipelines,
deployment targets, and cost management.

   fabrica setup              Guided first-time provisioning
   fabrica doctor             Run diagnostic checks
   fabrica destroy            Tear down provisioned infrastructure
   fabrica config             View and modify fabrica.yaml configuration
   fabrica version            Print version information

Use --config to specify a config file (default is ./fabrica.yaml).
Use --verbose for detailed output, --json for machine-readable output.
Use --dry-run to preview operations without making changes.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./fabrica.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
}
