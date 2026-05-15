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
		fmt.Printf("Fabrica %s\n", fabricav.Version)
		if fabricav.Commit != "" {
			fmt.Printf("Commit: %s\n", fabricav.Commit)
		}
		fmt.Printf("Go:     %s\n", runtime.Version())
		fmt.Printf("OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return nil
	},
}
