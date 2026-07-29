package status

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "perforce"
	p4Port     = 1666
)

// StatusOutput is the JSON-serialisable view of a Perforce status.
type StatusOutput struct {
	modstatus.BaseStatusOutput
	Version      string `json:"version,omitempty"`
	P4PORT       string `json:"p4port,omitempty"`
	HelixCore    string `json:"helixCore,omitempty"`
	LastBackupId string `json:"lastBackupId,omitempty"`
	LastBackupAt string `json:"lastBackupAt,omitempty"`
}

// New returns the "perforce status" subcommand. Global flags (--json) are
// resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show Perforce Helix Core status",
		Long: `Show the current status of the Perforce Helix Core server.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes TCP port 1666 to verify
that Helix Core is accepting connections.

When the server transitions from provisioning to ready for the first time,
status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until Helix Core is reachable
(times out after 10 minutes).`,
		ModuleName:  moduleName,
		DisplayName: "Perforce",
		Resolve: func(globals.Runtime) modstatus.RuntimeSpec {
			return modstatus.RuntimeSpec{
				ProbePort: p4Port,
				Renderer: modstatus.NewRenderer(
					"Perforce", "fabrica perforce create", printText, printJSON,
				),
			}
		},
	}, runtimeSource, optionsSource, out)
}

func printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Perforce Helix Core")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  P4PORT:        tcp:%s:%d\n", info.PrivateIP, p4Port)
	}
	modstatus.WriteSecurityGroup(out, info.SGID)
	if info.Version != "" {
		fmt.Fprintf(out, "  Version:       %s\n", info.Version)
	}
	modstatus.WriteProbeStatusText(out, info, "Helix Core", info.Version)
	printBackupStatus(out, info)
}

func printBackupStatus(out io.Writer, info modstatus.Info) {
	if info.LastBackupAt != "" {
		label := info.LastBackupAt
		if info.LastBackupId != "" {
			label += fmt.Sprintf(" (%s)", info.LastBackupId)
		}
		fmt.Fprintf(out, "  Last backup:   %s\n", label)
		return
	}
	fmt.Fprintln(out, "  Last backup:   never")
}

func printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{
		Version:      info.Version,
		LastBackupId: info.LastBackupId,
		LastBackupAt: info.LastBackupAt,
	}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.P4PORT = fmt.Sprintf("tcp:%s:%d", info.PrivateIP, p4Port)
	}
	o.HelixCore = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}
