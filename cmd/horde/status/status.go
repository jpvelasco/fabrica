package status

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "horde"

	defaultPort     = 5000
	defaultGRPCPort = 5002
)

// StatusOutput is the JSON-serialisable view of a Horde status.
type StatusOutput struct {
	modstatus.BaseStatusOutput
	HordeURL    string `json:"hordeUrl,omitempty"`
	HordeGRPC   string `json:"hordeGrpc,omitempty"`
	HordeStatus string `json:"hordeStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}

// renderer implements modstatus.Renderer for Horde-specific output. It carries
// the resolved HTTP/gRPC ports so endpoint URLs render correctly.
type renderer struct {
	port     int
	grpcPort int
}

// New returns the "horde status" subcommand. Global flags (--json) are
// resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Horde coordinator status",
		Long: `Show the current status of the Horde build coordinator.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes TCP port 5000 to verify
that the Horde HTTP API is accepting connections.

When the coordinator transitions from provisioning to ready for the first time,
status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until Horde is reachable
(times out after 10 minutes).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			port, grpcPort := resolvePorts(rt)
			c := modstatus.Command{
				Spec: modstatus.Spec{
					ModuleName:  moduleName,
					ProbePort:   port,
					DisplayName: "Horde",
				},
				Renderer:   renderer{port: port, grpcPort: grpcPort},
				Runtime:    rt,
				JSONOut:    opts.JSONOutput,
				Wait:       wait,
				Out:        out,
				ReadState:  func() (*fabricastate.State, error) { return readState(rt) },
				WriteState: fabricastate.WriteState,
				ProbeTCP:   modstatus.DefaultProbeTCP,
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

// resolvePorts returns the configured Horde HTTP and gRPC ports, falling back
// to defaults when unset.
func resolvePorts(rt globals.Runtime) (port, grpcPort int) {
	port, grpcPort = defaultPort, defaultGRPCPort
	if rt.Config != nil {
		if rt.Config.Horde.Port > 0 {
			port = rt.Config.Horde.Port
		}
		if rt.Config.Horde.GRPCPort > 0 {
			grpcPort = rt.Config.Horde.GRPCPort
		}
	}
	return port, grpcPort
}

func (renderer) NotProvisioned(out io.Writer, jsonOut bool) {
	if jsonOut {
		modstatus.WriteNotProvisionedJSON(out)
		return
	}
	modstatus.WriteNotProvisionedText(out, "Horde", "fabrica horde create")
}

func (r renderer) Result(out io.Writer, info modstatus.Info, jsonOut bool) {
	if jsonOut {
		r.printJSON(out, info)
		return
	}
	r.printText(out, info)
}

func (r renderer) printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Horde build coordinator")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  Horde HTTP:    http://%s:%d\n", info.PrivateIP, r.port)
		fmt.Fprintf(out, "  Horde gRPC:    %s:%d\n", info.PrivateIP, r.grpcPort)
	}
	modstatus.WriteSecurityGroup(out, info.SGID)
	modstatus.WriteProbeStatusText(out, info, "Horde", "")
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.HordeURL = fmt.Sprintf("http://%s:%d", info.PrivateIP, r.port)
		o.HordeGRPC = fmt.Sprintf("%s:%d", info.PrivateIP, r.grpcPort)
	}
	o.HordeStatus = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	return provision.ReadState(rt)
}
