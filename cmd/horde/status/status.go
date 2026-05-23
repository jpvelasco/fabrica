package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "horde"

	probeTimeout = 3 * time.Second
	waitInterval = 15 * time.Second
	waitDeadline = 10 * time.Minute

	defaultPort     = 5000
	defaultGRPCPort = 5002
)

type statusInfo struct {
	moduleStatus string
	instanceID   string
	sgID         string

	instanceType  string
	privateIP     string
	instanceState string
	port          int
	grpcPort      int

	hordeReachable      bool
	hordeProbeAttempted bool
}

// StatusOutput is the JSON-serialisable view of statusInfo.
type StatusOutput struct {
	Provisioned  bool   `json:"provisioned"`
	Status       string `json:"status"`
	InstanceID   string `json:"instanceId,omitempty"`
	SGID         string `json:"sgId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
	HordeURL     string `json:"hordeUrl,omitempty"`
	HordeGRPC    string `json:"hordeGrpc,omitempty"`
	HordeStatus  string `json:"hordeStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}

type command struct {
	runtime globals.Runtime
	jsonOut bool
	wait    bool
	out     io.Writer

	// seams for testing
	readState   func() (*fabricastate.State, error)
	writeState  func(*fabricastate.State) error
	getResource func(ctx context.Context, r *cloud.Resource) error
	probeTCP    func(address string) bool
	sleep       func(d time.Duration)
	now         func() time.Time
}

// New returns the "horde status" subcommand.
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
			c := command{
				runtime: rt,
				jsonOut: opts.JSONOutput,
				wait:    wait,
				out:     out,
			}
			c.readState = c.defaultReadState
			c.writeState = c.defaultWriteState
			c.probeTCP = defaultProbeTCP
			c.sleep = time.Sleep
			c.now = time.Now
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Poll until ready or 10 minutes elapsed")
	return cmd
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		c.printNotProvisioned()
		return nil
	}

	if c.wait {
		return c.pollUntilReady(ctx, st, m)
	}

	info, err := c.buildInfo(ctx, m)
	if err != nil {
		return err
	}

	if info.hordeProbeAttempted && info.hordeReachable && m.Status != "ready" {
		m.Status = "ready"
		info.moduleStatus = "ready"
		st.UpsertModule(moduleName, m.Version, "ready", m.Resources)
		if err := c.writeState(st); err != nil {
			fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
		}
	}

	c.printResult(info)
	return nil
}

func (c command) pollUntilReady(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState) error {
	deadline := c.now().Add(waitDeadline)
	for {
		info, err := c.buildInfo(ctx, m)
		if err != nil {
			return err
		}

		if info.hordeProbeAttempted && info.hordeReachable {
			m.Status = "ready"
			info.moduleStatus = "ready"
			st.UpsertModule(moduleName, m.Version, "ready", m.Resources)
			if err := c.writeState(st); err != nil {
				fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
			}
			c.printResult(info)
			return nil
		}

		c.printResult(info)

		if c.now().After(deadline) {
			fmt.Fprintln(c.out, "Timed out waiting for Horde to become ready (10 minutes).")
			return nil
		}

		fmt.Fprintf(c.out, "Waiting %s before next check...\n\n", waitInterval)
		c.sleep(waitInterval)
	}
}

func (c command) buildInfo(ctx context.Context, m *fabricastate.ModuleState) (statusInfo, error) {
	port := c.runtime.Config.Horde.Port
	if port <= 0 {
		port = defaultPort
	}
	grpcPort := c.runtime.Config.Horde.GRPCPort
	if grpcPort <= 0 {
		grpcPort = defaultGRPCPort
	}

	info := statusInfo{
		moduleStatus: m.Status,
		port:         port,
		grpcPort:     grpcPort,
	}

	sgRes, hasSG := resourceByType(m, "AWS::EC2::SecurityGroup")
	if hasSG {
		info.sgID = sgRes.Identifier
	}

	instRes, hasInst := resourceByType(m, "AWS::EC2::Instance")
	if !hasInst {
		return info, nil
	}
	info.instanceID = instRes.Identifier

	if c.getResource != nil && info.instanceID != "" {
		r := &cloud.Resource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: info.instanceID,
		}
		if err := c.getResource(ctx, r); err != nil {
			return info, fmt.Errorf("querying instance %s via Cloud Control: %w", info.instanceID, err)
		}
		parseInstanceActualState(r, &info)
	}

	if info.privateIP != "" {
		info.hordeProbeAttempted = true
		info.hordeReachable = c.probeTCP(fmt.Sprintf("%s:%d", info.privateIP, port))
	}

	return info, nil
}

func parseInstanceActualState(r *cloud.Resource, info *statusInfo) {
	if len(r.ActualState) == 0 {
		return
	}
	var actual struct {
		InstanceType     string `json:"InstanceType"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
		State            struct {
			Name string `json:"Name"`
		} `json:"State"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil {
		return
	}
	info.instanceType = actual.InstanceType
	info.privateIP = actual.PrivateIPAddress
	info.instanceState = actual.State.Name
}

func (c command) printNotProvisioned() {
	if c.jsonOut {
		out := StatusOutput{Provisioned: false, Status: "not_provisioned"}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(c.out, string(data))
		return
	}
	fmt.Fprintln(c.out, "Horde is not provisioned. Run 'fabrica horde create' to set it up.")
}

func (c command) printResult(info statusInfo) {
	if c.jsonOut {
		c.printJSON(info)
		return
	}
	c.printText(info)
}

func (c command) printText(info statusInfo) {
	fmt.Fprintln(c.out, "Horde build coordinator")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Status:        %s\n", info.moduleStatus)

	if info.instanceID != "" {
		label := info.instanceID
		if info.instanceState != "" {
			label += fmt.Sprintf("  (%s)", info.instanceState)
		}
		fmt.Fprintf(c.out, "  Instance ID:   %s\n", label)
	}

	if info.instanceType != "" {
		fmt.Fprintf(c.out, "  Instance type: %s\n", info.instanceType)
	}

	if info.privateIP != "" {
		fmt.Fprintf(c.out, "  Private IP:    %s\n", info.privateIP)
		fmt.Fprintf(c.out, "  Horde HTTP:    http://%s:%d\n", info.privateIP, info.port)
		fmt.Fprintf(c.out, "  Horde gRPC:    %s:%d\n", info.privateIP, info.grpcPort)
	}

	if info.sgID != "" {
		fmt.Fprintf(c.out, "  Security Group: %s\n", info.sgID)
	}

	if info.hordeProbeAttempted {
		if info.hordeReachable {
			fmt.Fprintln(c.out, "  Horde:         responding")
		} else {
			fmt.Fprintln(c.out, "  Horde:         unreachable from this machine (check VPN/network)")
		}
	} else if info.moduleStatus == "provisioning" {
		fmt.Fprintln(c.out, "  Horde:         setting up... (~3 min from launch)")
	}
}

func (c command) printJSON(info statusInfo) {
	out := StatusOutput{
		Provisioned:  true,
		Status:       info.moduleStatus,
		InstanceID:   info.instanceID,
		SGID:         info.sgID,
		InstanceType: info.instanceType,
		PrivateIP:    info.privateIP,
	}
	if info.privateIP != "" {
		out.HordeURL = fmt.Sprintf("http://%s:%d", info.privateIP, info.port)
		out.HordeGRPC = fmt.Sprintf("%s:%d", info.privateIP, info.grpcPort)
	}
	if info.hordeProbeAttempted {
		if info.hordeReachable {
			out.HordeStatus = "responding"
		} else {
			out.HordeStatus = "unreachable"
		}
	} else if info.moduleStatus == "provisioning" {
		out.HordeStatus = "setting up"
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}

func (c command) defaultWriteState(st *fabricastate.State) error {
	return fabricastate.WriteState(st)
}

func defaultProbeTCP(address string) bool {
	conn, err := net.DialTimeout("tcp", address, probeTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func resourceByType(m *fabricastate.ModuleState, typeName string) (fabricastate.ModuleResource, bool) {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
}
