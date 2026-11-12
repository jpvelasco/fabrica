package status

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/hordeapi"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "horde"

	defaultPort     = horde.DefaultPort
	defaultGRPCPort = horde.DefaultGRPCPort
)

// StatusOutput is the JSON-serialisable view of a Horde status.
type StatusOutput struct {
	modstatus.BaseStatusOutput
	HordeURL    string `json:"hordeUrl,omitempty"`
	HordeGRPC   string `json:"hordeGrpc,omitempty"`
	HordeStatus string `json:"hordeStatus,omitempty"` // "responding" | "unreachable" | "setting up"
	ProbeError  string `json:"probeError,omitempty"`
}

// renderer implements modstatus.Renderer for Horde-specific output. It carries
// the resolved HTTP/gRPC ports so endpoint URLs render correctly.
type renderer struct {
	port       int
	grpcPort   int
	connection horde.Connection
}

// New returns the "horde status" subcommand. Global flags (--json) are
// resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return modstatus.NewCobraCommand(modstatus.CobraSpec{
		Short: "Show Horde coordinator status",
		Long: `Show the current status of the Horde build coordinator.

Reads recorded module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). HTTP mode checks TCP reachability.
TLS mode verifies the HTTPS certificate and reported listener configuration.

When the coordinator transitions from provisioning to ready for the first time,
status updates readiness under the state lock after re-reading current state.

Use --wait / -w to poll every 15 seconds until Horde is reachable
(times out after 10 minutes).`,
		ModuleName:  moduleName,
		DisplayName: "Horde",
		Resolve: func(rt globals.Runtime, cmdCtx context.Context) modstatus.RuntimeSpec {
			// Only used for the not-provisioned renderer. Present modules
			// resolve their connection once through ResolveState.
			r := renderer{}
			return modstatus.RuntimeSpec{
				Renderer: modstatus.NewRenderer(
					"Horde", "fabrica horde create", r.printText, r.printJSON,
				),
			}
		},
		ResolveState: resolveState,
	}, runtimeSource, optionsSource, out)
}

func resolveState(rt globals.Runtime, _ context.Context, m *state.ModuleState) (modstatus.RuntimeSpec, error) {
	connection, err := horde.ResolveConnection(rt.Config.Horde, m)
	if err != nil {
		return modstatus.RuntimeSpec{}, err
	}
	r := renderer{port: connection.Port, grpcPort: connection.GRPCPort, connection: connection}
	spec := modstatus.RuntimeSpec{ProbePort: connection.Port, Renderer: modstatus.NewRenderer("Horde", "fabrica horde create", r.printText, r.printJSON), ResourceByType: horde.CoordinatorResource}
	if connection.TLS {
		client, err := hordeapi.NewClient(connection, rt.ConfigPath, rt.Config.Horde.ClientCAPath, "", 3*time.Second)
		if err != nil {
			return spec, err
		}
		spec.ProbeContext = func(ctx context.Context, ip string) error { return hordeapi.Probe(ctx, client, connection, ip) }
	}
	spec.Ready = func(ctx context.Context, observed *state.ModuleState) error {
		ctx, release, err := provision.AcquireStateLock(ctx, rt, "horde status ready")
		if err != nil {
			return err
		}
		defer release()
		st, err := provision.ReadState(ctx, rt)
		if err != nil {
			return err
		}
		current := st.GetModule(moduleName)
		oldInst, oldOK := horde.CoordinatorResource(observed, cloud.TypeAWSEC2Instance)
		inst, ok := horde.CoordinatorResource(current, cloud.TypeAWSEC2Instance)
		if !oldOK || !ok || inst.Identifier != oldInst.Identifier {
			return fmt.Errorf("Horde instance changed during readiness probe; run status again")
		}
		now, err := horde.ResolveConnection(rt.Config.Horde, current)
		if err != nil {
			return err
		}
		if now != connection {
			return fmt.Errorf("Horde connection changed during readiness probe; run status again")
		}
		st.UpsertModule(moduleName, current.Version, "ready", current.Resources)
		return provision.StateWriter(ctx, rt)(st)
	}
	return spec, nil
}

func (r renderer) printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Horde build coordinator")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	modstatus.WriteCommonFields(out, info)
	if info.PrivateIP != "" {
		url, grpc := r.endpoints(info.PrivateIP)
		fmt.Fprintf(out, "  Horde API:     %s\n", url)
		fmt.Fprintf(out, "  Horde gRPC:    %s\n", grpc)
	}
	modstatus.WriteSecurityGroup(out, info.SGID)
	modstatus.WriteProbeStatusText(out, info, "Horde", "")
	if info.ProbeError != "" {
		fmt.Fprintf(out, "  Probe error:   %s\n", info.ProbeError)
	}
}

func (r renderer) printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{}
	o.BaseStatusOutput = modstatus.NewBaseStatusOutput(info)
	if info.PrivateIP != "" {
		o.HordeURL, o.HordeGRPC = r.endpoints(info.PrivateIP)
	}
	o.HordeStatus = modstatus.ProbeStatus(info)
	o.ProbeError = info.ProbeError
	modstatus.WriteJSON(out, o)
}

func (r renderer) endpoints(ip string) (string, string) {
	c := r.connection
	if c.Version == 0 {
		c.Port, c.GRPCPort = r.port, r.grpcPort
	}
	if c.TLS {
		return c.URL(ip), c.URL(ip)
	}
	return c.URL(ip), fmt.Sprintf("%s:%d", ip, c.GRPCPort)
}
