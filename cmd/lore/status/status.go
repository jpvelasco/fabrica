package status

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "lore"
)

// StatusOutput is the JSON-serialisable view of a Lore status.
type StatusOutput struct {
	modstatus.BaseStatusOutput
	LoreURL    string `json:"loreUrl,omitempty"`
	LoreGRPC   string `json:"loreGrpc,omitempty"`
	LoreStatus string `json:"loreStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}

// renderer implements modstatus.Renderer for Lore-specific output.
type renderer struct {
	grpcPort int
	httpPort int
}

// New returns the "lore status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show Lore server status",
		Long: `Show the current status of the Lore loreserver.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes HTTP GET /health_check on
port 41339 to verify that loreserver is accepting connections.

When the server transitions from provisioning to ready for the first time,
status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until Lore is reachable
(times out after 10 minutes).`,
		ModuleName:  moduleName,
		DisplayName: "Lore",
		Resolve: func(globals.Runtime) modstatus.RuntimeSpec {
			r := renderer{
				grpcPort: lore.DefaultGRPCPort,
				httpPort: lore.DefaultHTTPPort,
			}
			return modstatus.RuntimeSpec{
				ProbePort: lore.DefaultHTTPPort,
				Renderer: modstatus.NewRenderer(
					"Lore", "fabrica lore create", r.printText, r.printJSON,
				),
				Probe: modstatus.HTTPProbe("/health_check"),
			}
		},
	}, runtimeSource, optionsSource, out)
}

func (r renderer) printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Lore loreserver")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  Lore HTTP:     http://%s:%d/health_check\n", info.PrivateIP, r.httpPort)
		fmt.Fprintf(out, "  Lore gRPC:     %s:%d (tcp+udp)\n", info.PrivateIP, r.grpcPort)
	}
	modstatus.WriteSecurityGroup(out, info.SGID)
	modstatus.WriteProbeStatusText(out, info, "Lore", "")
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.LoreURL = fmt.Sprintf("http://%s:%d/health_check", info.PrivateIP, r.httpPort)
		o.LoreGRPC = fmt.Sprintf("%s:%d", info.PrivateIP, r.grpcPort)
	}
	o.LoreStatus = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}
