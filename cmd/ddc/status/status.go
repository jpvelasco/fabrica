package status

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "ddc"
)

// StatusOutput is the JSON view of DDC status (single home-region).
type StatusOutput struct {
	modstatus.BaseStatusOutput
	PublicURL string `json:"publicUrl,omitempty"`
	DDCStatus string `json:"ddcStatus,omitempty"`
	Backend   string `json:"backend,omitempty"`
}

type renderer struct {
	publicPort int
	backend    string
}

// New returns the "ddc status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show DDC status and endpoints",
		Long: `Show the status of the home-region Unreal Cloud DDC deployment.

Reads local module state, queries the DDC EC2 instance, and optionally probes
HTTP GET /health/ready on the public API port.

V1 is single home-region only — no multi-region edge list.`,
		ModuleName:  moduleName,
		DisplayName: "DDC",
		Resolve: func(rt globals.Runtime) modstatus.RuntimeSpec {
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
			r := renderer{publicPort: port, backend: backend}
			return modstatus.RuntimeSpec{
				ProbePort: port,
				Renderer: modstatus.NewRenderer(
					"DDC", "fabrica ddc setup", r.printText, r.printJSON,
				),
				Probe: modstatus.HTTPProbe("/health/ready"),
			}
		},
	}, runtimeSource, optionsSource, out)
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
	modstatus.WriteSecurityGroup(out, info.SGID)
	modstatus.WriteProbeStatusText(out, info, "DDC", "")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Note: V1 is single home-region only (no multi-region edges).")
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{Backend: r.backend}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.PublicURL = fmt.Sprintf("http://%s:%d", info.PrivateIP, r.publicPort)
	}
	o.DDCStatus = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}
