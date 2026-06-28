package status

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "perforce"
	p4Port     = 1666
)

// StatusOutput is the JSON-serialisable view of a Perforce status.
type StatusOutput struct {
	Provisioned  bool   `json:"provisioned"`
	Status       string `json:"status"`
	InstanceID   string `json:"instanceId,omitempty"`
	SGID         string `json:"sgId,omitempty"`
	Version      string `json:"version,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
	P4PORT       string `json:"p4port,omitempty"`
	HelixCore    string `json:"helixCore,omitempty"`
}

// renderer implements modstatus.Renderer for Perforce-specific output.
type renderer struct{}

// New returns the "perforce status" subcommand. Global flags (--json) are
// resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Perforce Helix Core status",
		Long: `Show the current status of the Perforce Helix Core server.

Reads local module state and queries the EC2 instance for live details
(instance type, private IP, EC2 state). Probes TCP port 1666 to verify
that Helix Core is accepting connections.

When the server transitions from provisioning to ready for the first time,
status automatically updates the local state file.

Use --wait / -w to poll every 15 seconds until Helix Core is reachable
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
					ProbePort:   p4Port,
					DisplayName: "Perforce",
				},
				Renderer:   renderer{},
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

func (renderer) NotProvisioned(out io.Writer, jsonOut bool) {
	if jsonOut {
		o := StatusOutput{Provisioned: false, Status: "not_provisioned"}
		data, _ := json.MarshalIndent(o, "", "  ")
		fmt.Fprintln(out, string(data))
		return
	}
	fmt.Fprintln(out, "Perforce is not provisioned. Run 'fabrica perforce create' to set it up.")
}

func (renderer) Result(out io.Writer, info modstatus.Info, jsonOut bool) {
	if jsonOut {
		printJSON(out, info)
		return
	}
	printText(out, info)
}

func printText(out io.Writer, info modstatus.Info) {
	fmt.Fprintln(out, "Perforce Helix Core")
	fmt.Fprintln(out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(out, "  Status:        %s\n", info.ModuleStatus)

	if info.InstanceID != "" {
		label := info.InstanceID
		if info.InstanceState != "" {
			label += fmt.Sprintf("  (%s)", info.InstanceState)
		}
		fmt.Fprintf(out, "  Instance ID:   %s\n", label)
	}

	if info.InstanceType != "" {
		fmt.Fprintf(out, "  Instance type: %s\n", info.InstanceType)
	}

	if info.PrivateIP != "" {
		fmt.Fprintf(out, "  Private IP:    %s\n", info.PrivateIP)
		fmt.Fprintf(out, "  P4PORT:        tcp:%s:%d\n", info.PrivateIP, p4Port)
	}

	if info.SGID != "" {
		fmt.Fprintf(out, "  Security Group: %s\n", info.SGID)
	}

	if info.Version != "" {
		fmt.Fprintf(out, "  Version:       %s\n", info.Version)
	}

	if info.ProbeAttempted {
		if info.Reachable {
			fmt.Fprintf(out, "  Helix Core:    %s (responding)\n", info.Version)
		} else {
			fmt.Fprintln(out, "  Helix Core:    unreachable from this machine (check VPN/network)")
		}
	} else if info.ModuleStatus == "provisioning" {
		fmt.Fprintln(out, "  Helix Core:    setting up... (~3 min from launch)")
	}
}

func printJSON(out io.Writer, info modstatus.Info) {
	o := StatusOutput{
		Provisioned:  true,
		Status:       info.ModuleStatus,
		InstanceID:   info.InstanceID,
		SGID:         info.SGID,
		Version:      info.Version,
		InstanceType: info.InstanceType,
		PrivateIP:    info.PrivateIP,
	}
	if info.PrivateIP != "" {
		o.P4PORT = fmt.Sprintf("tcp:%s:%d", info.PrivateIP, p4Port)
	}
	if info.ProbeAttempted {
		if info.Reachable {
			o.HelixCore = "responding"
		} else {
			o.HelixCore = "unreachable"
		}
	} else if info.ModuleStatus == "provisioning" {
		o.HelixCore = "setting up"
	}
	data, _ := json.MarshalIndent(o, "", "  ")
	fmt.Fprintln(out, string(data))
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	account, region := "", ""
	if rt.Config != nil {
		account = rt.Config.Cloud.AWS.AccountID
		region = rt.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
