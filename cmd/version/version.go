package version

import (
	"fmt"
	"runtime"

	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:          "version",
	Short:        "Show version information",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintln(cmd.OutOrStdout(), "Fabrica", fabricav.String())
		fmt.Fprintf(cmd.OutOrStdout(), "Go:     %s\n", runtime.Version())
		fmt.Fprintf(cmd.OutOrStdout(), "OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}
