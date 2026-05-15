package root

import (
	"context"
	"os"
	"os/signal"

	"github.com/jpvelasco/fabrica/cmd/configcmd"
	"github.com/jpvelasco/fabrica/cmd/destroy"
	"github.com/jpvelasco/fabrica/cmd/doctor"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/setup"
	"github.com/jpvelasco/fabrica/cmd/version"
	_ "github.com/jpvelasco/fabrica/internal/cloud/aws"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:          "fabrica",
	SilenceUsage: true,
	Short:        "Studio infrastructure as code — AWS",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return globals.Init(config.Path(cfgFile, globals.Profile))
	},
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Path to config file (default: ./fabrica.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&globals.Verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVarP(&globals.JSONOutput, "json", "j", false, "Output in JSON format")
	rootCmd.PersistentFlags().BoolVarP(&globals.DryRun, "dry-run", "d", false, "Show what would be done without making changes")
	rootCmd.PersistentFlags().BoolVarP(&globals.AssumeYes, "yes", "y", false, "Skip confirmation prompts")
	rootCmd.PersistentFlags().StringVarP(&globals.Profile, "profile", "p", "", "Fabrica config profile to use (loads fabrica-<profile>.yaml)")

	rootCmd.AddCommand(version.Cmd)
	rootCmd.AddCommand(doctor.Cmd)
	rootCmd.AddCommand(setup.Cmd)
	rootCmd.AddCommand(destroy.Cmd)
	rootCmd.AddCommand(configcmd.Cmd)
}
