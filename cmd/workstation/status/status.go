// Package status implements "fabrica workstation status" — it reads local
// module state, queries the EC2 instance for live details, and probes TCP
// 8443 to verify the NICE DCV server is accepting connections.
package status

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/workstation"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
)

// StatusOutput is the JSON-serialisable view of a workstation status.
type StatusOutput struct {
	modstatus.BaseStatusOutput
	DCVURL    string `json:"dcvUrl,omitempty"`
	DCVStatus string `json:"dcvStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}

// New returns the "workstation status" subcommand. Global flags (--json) are
// resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show NICE DCV workstation status",
		Long: `Show the current status of the NICE DCV cloud workstation.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes TCP port 8443 to verify
that the DCV server is accepting connections.

When the workstation transitions from provisioning to ready for the first
time, status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until the DCV server is reachable
(times out after 10 minutes).`,
		ModuleName:  moduleName,
		DisplayName: "NICE DCV",
		Resolve: func(globals.Runtime, context.Context) modstatus.RuntimeSpec {
			return modstatus.RuntimeSpec{
				ProbePort: workstation.DefaultDCVPort,
				Renderer: modstatus.NewRenderer(
					"Workstation", "fabrica workstation create", printText, printJSON,
				),
			}
		},
	}, runtimeSource, optionsSource, out)
}

func printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "NICE DCV Workstation")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  DCV:           https://%s:%d\n", info.PrivateIP, workstation.DefaultDCVPort)
	}
	modstatus.WriteSecurityGroup(out, info.SGID)
	modstatus.WriteProbeStatusText(out, info, "DCV", "")
}

func printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.DCVURL = fmt.Sprintf("https://%s:%d", info.PrivateIP, workstation.DefaultDCVPort)
	}
	o.DCVStatus = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}
