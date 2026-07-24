package status

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/lore"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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
	var wait bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Lore server status",
		Long: `Show the current status of the Lore loreserver.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes HTTP GET /health_check on
port 41339 to verify that loreserver is accepting connections.

When the server transitions from provisioning to ready for the first time,
status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until Lore is reachable
(times out after 10 minutes).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := modstatus.Command{
				Spec: modstatus.Spec{
					ModuleName:  moduleName,
					ProbePort:   lore.DefaultHTTPPort,
					DisplayName: "Lore",
				},
				Renderer: renderer{
					grpcPort: lore.DefaultGRPCPort,
					httpPort: lore.DefaultHTTPPort,
				},
				Runtime:    rt,
				JSONOut:    opts.JSONOutput,
				Wait:       wait,
				Out:        out,
				ReadState:  func() (*fabricastate.State, error) { return readState(rt) },
				WriteState: fabricastate.WriteState,
				ProbeTCP:   probeHealthCheck,
				Sleep:      time.Sleep,
				Now:        time.Now,
			}
			if rt.Provider != nil {
				c.GetResource = rt.Provider.Resources().Get
			}
			return c.Run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Poll until ready or 10 minutes elapsed")
	return cmd
}

// probeHealthCheck performs GET http://address/health_check (address is host:port
// from modstatus). Returns true only on HTTP 200.
func probeHealthCheck(address string) bool {
	return modstatus.ProbeHTTP(address, "/health_check")
}

func (renderer) NotProvisioned(out io.Writer, jsonOut bool) {
	if jsonOut {
		modstatus.WriteNotProvisionedJSON(out)
		return
	}
	modstatus.WriteNotProvisionedText(out, "Lore", "fabrica lore create")
}

func (r renderer) Result(out io.Writer, info modstatus.Info, jsonOut bool) {
	if jsonOut {
		r.printJSON(out, info)
		return
	}
	r.printText(out, info)
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

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	return provision.ReadState(rt)
}
