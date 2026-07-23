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
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "ddc"
)

// StatusOutput is the JSON view of DDC status (single home-region).
type StatusOutput struct {
	Provisioned  bool   `json:"provisioned"`
	Status       string `json:"status"`
	InstanceID   string `json:"instanceId,omitempty"`
	SGID         string `json:"sgId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
	PublicURL    string `json:"publicUrl,omitempty"`
	DDCStatus    string `json:"ddcStatus,omitempty"`
	Backend      string `json:"backend,omitempty"`
}

type renderer struct {
	publicPort int
	backend    string
}

// New returns the "ddc status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show DDC status and endpoints",
		Long: `Show the status of the home-region Unreal Cloud DDC deployment.

Reads local module state, queries the DDC EC2 instance, and optionally probes
HTTP GET /health/ready on the public API port.

V1 is single home-region only — no multi-region edge list.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			port := ddc.DefaultPublicPort
			backend := ddc.BackendZen
			if rt.Config != nil {
				if rt.Config.DDC.PublicPort > 0 {
					port = rt.Config.DDC.PublicPort
				}
				if rt.Config.DDC.Backend != "" {
					backend = rt.Config.DDC.Backend
				}
			}
			c := modstatus.Command{
				Spec: modstatus.Spec{
					ModuleName:  moduleName,
					ProbePort:   port,
					DisplayName: "DDC",
				},
				Renderer: renderer{publicPort: port, backend: backend},
				Runtime:  rt,
				JSONOut:  opts.JSONOutput,
				Wait:     wait,
				Out:      out,
				ReadState: func() (*fabricastate.State, error) {
					return readState(rt)
				},
				WriteState: fabricastate.WriteState,
				ProbeTCP:   probeReady,
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

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	return provision.ReadState(rt)
}

// probeReady performs GET http://host:port/health/ready.
func probeReady(address string) bool {
	return modstatus.ProbeHTTP(address, "/health/ready")
}

func (renderer) NotProvisioned(out io.Writer, jsonOut bool) {
	if jsonOut {
		modstatus.WriteNotProvisionedJSON(out)
		return
	}
	fmt.Fprintln(out, "DDC is not provisioned. Run 'fabrica ddc setup' to set it up.")
}

func (r renderer) Result(out io.Writer, info modstatus.Info, jsonOut bool) {
	if jsonOut {
		r.printJSON(out, info)
		return
	}
	r.printText(out, info)
}

func (r renderer) printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Distributed DDC (home region)")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)
	fmt.Fprintf(out, "  Backend:       %s\n", r.backend)
	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  Public URL:    http://%s:%d\n", info.PrivateIP, r.publicPort)
		fmt.Fprintf(out, "  Health:        http://%s:%d/health/ready\n", info.PrivateIP, r.publicPort)
	}
	if info.SGID != "" {
		fmt.Fprintf(out, "  Security Group: %s\n", info.SGID)
	}
	if info.ProbeAttempted {
		if info.Reachable {
			fmt.Fprintln(out, "  DDC:           responding (/health/ready)")
		} else {
			fmt.Fprintln(out, "  DDC:           unreachable from this machine (check VPN/network)")
		}
	} else if info.ModuleStatus == "provisioning" {
		fmt.Fprintln(out, "  DDC:           setting up...")
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Note: V1 is single home-region only (no multi-region edges).")
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{
		Provisioned:  true,
		Status:       info.ModuleStatus,
		InstanceID:   info.InstanceID,
		SGID:         info.SGID,
		InstanceType: info.InstanceType,
		PrivateIP:    info.PrivateIP,
		Backend:      r.backend,
	}
	if info.PrivateIP != "" {
		o.PublicURL = fmt.Sprintf("http://%s:%d", info.PrivateIP, r.publicPort)
	}
	if info.ProbeAttempted {
		if info.Reachable {
			o.DDCStatus = "responding"
		} else {
			o.DDCStatus = "unreachable"
		}
	} else if info.ModuleStatus == "provisioning" {
		o.DDCStatus = "setting up"
	}
	data, _ := json.MarshalIndent(o, "", "  ")
	fmt.Fprintln(out, string(data))
}
