package root

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/jpvelasco/fabrica/cmd/configcmd"
	"github.com/jpvelasco/fabrica/cmd/destroy"
	"github.com/jpvelasco/fabrica/cmd/doctor"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde"
	"github.com/jpvelasco/fabrica/cmd/workstation"
	"github.com/jpvelasco/fabrica/cmd/perforce"
	"github.com/jpvelasco/fabrica/cmd/setup"
	"github.com/jpvelasco/fabrica/cmd/version"
	_ "github.com/jpvelasco/fabrica/internal/cloud/aws"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = New(os.Stdout)

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func New(out io.Writer) *cobra.Command {
	var cfgFile string
	var opts globals.Options
	var runtimeStore globals.Store

	cmd := &cobra.Command{
		Use:          "fabrica",
		SilenceUsage: true,
		Short:        "Studio infrastructure as code — AWS",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return runtimeStore.Init(config.Path(cfgFile, opts.Profile))
		},
	}

	runtimeSource := runtimeStore.Require
	optionsSource := func() globals.Options {
		return opts
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Path to config file (default: ./fabrica.yaml)")
	cmd.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false, "Enable verbose output")
	cmd.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "Output in JSON format")
	cmd.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "Show what would be done without making changes")
	cmd.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "Skip confirmation prompts")
	cmd.PersistentFlags().StringVarP(&opts.Profile, "profile", "p", "", "Fabrica config profile to use (loads fabrica-<profile>.yaml)")

	cmd.AddCommand(version.Cmd)
	cmd.AddCommand(doctor.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(configcmd.New(runtimeSource, out))
	cmd.AddCommand(perforce.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(horde.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(workstation.New(runtimeSource, optionsSource, out))

	return cmd
}
