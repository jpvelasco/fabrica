package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "perforce"

	probeTimeout = 3 * time.Second
	waitInterval = 15 * time.Second
	waitDeadline = 10 * time.Minute
)

// statusInfo holds everything known about the Perforce module at query time.
type statusInfo struct {
	// from state
	moduleStatus string
	instanceID   string
	sgID         string
	version      string

	// from provider.Resources().Get (may be empty when Cloud Control is stubbed)
	instanceType  string
	privateIP     string
	instanceState string // e.g. "running", "stopped"

	// from TCP probe (only attempted when privateIP is known)
	p4Reachable      bool
	p4ProbeAttempted bool
}

// StatusOutput is the JSON-serialisable view of statusInfo.
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
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Perforce Helix Core status",
		Long: `Show the current status of the Perforce Helix Core server.

Reads module state and queries the EC2 instance via Cloud Control.
Probes TCP port 1666 to determine Helix Core readiness.

Use --wait / -w to poll every 15 seconds until ready (up to 10 minutes).`,
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

	// Transition provisioning → ready when TCP probe first succeeds.
	if info.p4ProbeAttempted && info.p4Reachable && m.Status != "ready" {
		m.Status = "ready"
		info.moduleStatus = "ready"
		st.UpsertModule(moduleName, m.Version, "ready", m.Resources)
		_ = c.writeState(st) // best-effort; don't fail status on write error
	}

	c.printResult(info)
	return nil
}

func (c command) pollUntilReady(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState) error {
	deadline := time.Now().Add(waitDeadline)
	for {
		info, err := c.buildInfo(ctx, m)
		if err != nil {
			return err
		}

		if info.p4ProbeAttempted && info.p4Reachable {
			m.Status = "ready"
			info.moduleStatus = "ready"
			st.UpsertModule(moduleName, m.Version, "ready", m.Resources)
			_ = c.writeState(st)
			c.printResult(info)
			return nil
		}

		c.printResult(info)

		if time.Now().After(deadline) {
			fmt.Fprintln(c.out, "Timed out waiting for Perforce to become ready (10 minutes).")
			return nil
		}

		fmt.Fprintf(c.out, "Waiting %s before next check...\n\n", waitInterval)
		c.sleep(waitInterval)
	}
}

// buildInfo queries Cloud Control and probes TCP to produce a statusInfo.
func (c command) buildInfo(ctx context.Context, m *fabricastate.ModuleState) (statusInfo, error) {
	info := statusInfo{
		moduleStatus: m.Status,
		version:      m.Version,
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

	// Query Cloud Control for live instance details.
	if c.getResource != nil && info.instanceID != "" {
		r := &cloud.Resource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: info.instanceID,
		}
		if err := c.getResource(ctx, r); err != nil {
			return info, fmt.Errorf("querying instance %s: %w", info.instanceID, err)
		}
		parseInstanceActualState(r, &info)
	}

	// Probe TCP 1666 only if we have a private IP.
	if info.privateIP != "" {
		info.p4ProbeAttempted = true
		info.p4Reachable = c.probeTCP(info.privateIP + ":1666")
	}

	return info, nil
}

// parseInstanceActualState extracts fields from the Cloud Control ActualState JSON.
// Silently ignores unparseable or nil ActualState (Cloud Control is currently stubbed).
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
	fmt.Fprintln(c.out, "Perforce is not provisioned. Run 'fabrica perforce create' to set it up.")
}

func (c command) printResult(info statusInfo) {
	if c.jsonOut {
		c.printJSON(info)
		return
	}
	c.printText(info)
}

func (c command) printText(info statusInfo) {
	fmt.Fprintln(c.out, "Perforce Helix Core")
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
		fmt.Fprintf(c.out, "  P4PORT:        tcp:%s:1666\n", info.privateIP)
	}

	if info.sgID != "" {
		fmt.Fprintf(c.out, "  Security Group: %s\n", info.sgID)
	}

	if info.version != "" {
		fmt.Fprintf(c.out, "  Version:       %s\n", info.version)
	}

	if info.p4ProbeAttempted {
		if info.p4Reachable {
			fmt.Fprintf(c.out, "  Helix Core:    %s (responding)\n", info.version)
		} else {
			fmt.Fprintln(c.out, "  Helix Core:    unreachable from this machine (check VPN/network)")
		}
	} else if info.moduleStatus == "provisioning" {
		fmt.Fprintln(c.out, "  Helix Core:    setting up... (~3 min from launch)")
	}
}

func (c command) printJSON(info statusInfo) {
	out := StatusOutput{
		Provisioned:  true,
		Status:       info.moduleStatus,
		InstanceID:   info.instanceID,
		SGID:         info.sgID,
		Version:      info.version,
		InstanceType: info.instanceType,
		PrivateIP:    info.privateIP,
	}
	if info.privateIP != "" {
		out.P4PORT = fmt.Sprintf("tcp:%s:1666", info.privateIP)
	}
	if info.p4ProbeAttempted {
		if info.p4Reachable {
			out.HelixCore = "responding"
		} else {
			out.HelixCore = "unreachable"
		}
	} else if info.moduleStatus == "provisioning" {
		out.HelixCore = "setting up"
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	data, err := os.ReadFile(".fabrica/state.json")
	if os.IsNotExist(err) {
		account := ""
		region := ""
		if c.runtime.Config != nil {
			region = c.runtime.Config.Cloud.AWS.Region
			account = c.runtime.Config.Cloud.AWS.AccountID
		}
		return fabricastate.NewState(account, region), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var st fabricastate.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &st, nil
}

func (c command) defaultWriteState(st *fabricastate.State) error {
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		return fmt.Errorf("creating .fabrica directory: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing state: %w", err)
	}
	return os.WriteFile(".fabrica/state.json", data, 0600)
}

func defaultProbeTCP(address string) bool {
	conn, err := net.DialTimeout("tcp", address, probeTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// resourceByType returns the first resource of the given TypeName from the module.
func resourceByType(m *fabricastate.ModuleState, typeName string) (fabricastate.ModuleResource, bool) {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
}
