package status

import (
	"encoding/json"
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
	Provisioned  bool   `json:"provisioned"`
	Status       string `json:"status"`
	InstanceID   string `json:"instanceId,omitempty"`
	SGID         string `json:"sgId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
	LoreURL      string `json:"loreUrl,omitempty"`
	LoreGRPC     string `json:"loreGrpc,omitempty"`
	LoreStatus   string `json:"loreStatus,omitempty"` // "responding" | "unreachable" | "setting up"
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
	fmt.Fprintln(out, "Lore is not provisioned. Run 'fabrica lore create' to set it up.")
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

	if info.SGID != "" {
		fmt.Fprintf(out, "  Security Group: %s\n", info.SGID)
	}

	if info.ProbeAttempted {
		if info.Reachable {
			fmt.Fprintln(out, "  Lore:          responding")
		} else {
			fmt.Fprintln(out, "  Lore:          unreachable from this machine (check VPN/network)")
		}
	} else if info.ModuleStatus == "provisioning" {
		fmt.Fprintln(out, "  Lore:          setting up... (~3 min from launch)")
	}
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{
		Provisioned:  true,
		Status:       info.ModuleStatus,
		InstanceID:   info.InstanceID,
		SGID:         info.SGID,
		InstanceType: info.InstanceType,
		PrivateIP:    info.PrivateIP,
	}
	if info.PrivateIP != "" {
		o.LoreURL = fmt.Sprintf("http://%s:%d/health_check", info.PrivateIP, r.httpPort)
		o.LoreGRPC = fmt.Sprintf("%s:%d", info.PrivateIP, r.grpcPort)
	}
	if info.ProbeAttempted {
		if info.Reachable {
			o.LoreStatus = "responding"
		} else {
			o.LoreStatus = "unreachable"
		}
	} else if info.ModuleStatus == "provisioning" {
		o.LoreStatus = "setting up"
	}
	data, _ := json.MarshalIndent(o, "", "  ")
	fmt.Fprintln(out, string(data))
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	return provision.ReadState(rt)
}
