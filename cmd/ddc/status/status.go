package status

import (
	"fmt"
	"io"
	"strings"

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

// EdgeOutput is the JSON view of one additional (edge) DDC region.
type EdgeOutput struct {
	Region       string `json:"region"`
	InstanceID   string `json:"instanceId"`
	SGID         string `json:"sgId"`
	InstanceType string `json:"instanceType"`
	Status       string `json:"status"`
}

// StatusOutput is the JSON view of DDC status (home + edge regions).
type StatusOutput struct {
	modstatus.BaseStatusOutput
	PublicURL string       `json:"publicUrl,omitempty"`
	DDCStatus string       `json:"ddcStatus,omitempty"`
	Backend   string       `json:"backend,omitempty"`
	Edges     []EdgeOutput `json:"edges,omitempty"`
}

type renderer struct {
	publicPort int
	backend    string
	readState  func() (*fabricastate.State, error)
}

// New returns the "ddc status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show DDC status and endpoints",
		Long: `Show the status of the Unreal Cloud DDC deployment: the home-region
coordinator/edge host plus any additional edge regions from 'ddc region add'.

Reads local module state, queries the home DDC EC2 instance, and optionally
probes HTTP GET /health/ready on the public API port. Edge regions are listed
from local state; live probes for edges are not available in this release.`,
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
			r := renderer{
				publicPort: port,
				backend:    backend,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
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

// homeSGID returns the home-region security group from state, ignoring edge
// SGs (which carry role=edge). Falls back to info.SGID when not found.
func (r renderer) homeSGID(info modstatus.Info) string {
	if r.readState == nil {
		return info.SGID
	}
	st, err := r.readState()
	if err != nil {
		return info.SGID
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return info.SGID
	}
	for _, res := range m.Resources {
		if res.TypeName != "AWS::EC2::SecurityGroup" {
			continue
		}
		if res.Properties != nil && res.Properties["role"] == ddc.RoleEdge {
			continue
		}
		return res.Identifier
	}
	return info.SGID
}

// edgeOutputs returns the sorted edge region list from state, or nil when no
// state is available.
func (r renderer) edgeOutputs() []EdgeOutput {
	if r.readState == nil {
		return nil
	}
	st, err := r.readState()
	if err != nil || st == nil {
		return nil
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return nil
	}
	edges := ddc.EdgeRegions(m.Resources, st.Region)
	if len(edges) == 0 {
		return nil
	}
	out := make([]EdgeOutput, 0, len(edges))
	for _, e := range edges {
		out = append(out, EdgeOutput{
			Region:       e.Region,
			InstanceID:   e.InstanceID,
			SGID:         e.SGID,
			InstanceType: e.InstanceType,
			Status:       "provisioned",
		})
	}
	return out
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
	modstatus.WriteSecurityGroup(out, r.homeSGID(info))
	modstatus.WriteProbeStatusText(out, info, "DDC", "")
	r.printEdgesText(out)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  Note: edge nodes are listed from local state; live probes")
	fmt.Fprintln(out, "  and cross-region replication checks are not available in this release.")
}

func (r renderer) printEdgesText(out io.Writer) {
	edges := r.edgeOutputs()
	if len(edges) == 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Edge regions:  none (single home-region deployment)")
		return
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  Edge regions:  %d\n", len(edges))
	for _, e := range edges {
		fmt.Fprintf(out, "    %s:\n", e.Region)
		fmt.Fprintf(out, "      Instance ID:   %s\n", e.InstanceID)
		fmt.Fprintf(out, "      Security Group: %s\n", e.SGID)
		if e.InstanceType != "" {
			fmt.Fprintf(out, "      Instance type: %s\n", e.InstanceType)
		}
		fmt.Fprintf(out, "      Status:        %s\n", e.Status)
	}
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{Backend: r.backend, Edges: r.edgeOutputs()}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	o.SGID = r.homeSGID(info)
	if info.PrivateIP != "" {
		o.PublicURL = fmt.Sprintf("http://%s:%d", info.PrivateIP, r.publicPort)
	}
	o.DDCStatus = modstatus.ProbeStatus(info)
	modstatus.WriteJSON(out, o)
}
