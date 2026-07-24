// Package modstatus is the shared engine behind the module status commands
// (perforce status, horde status). The orchestration — read state, query the
// EC2 instance via Cloud Control, TCP-probe for readiness, transition
// provisioning→ready, and optionally poll — is identical across modules. Only
// the rendering (which fields, labels, and JSON schema) differs, so each
// command supplies a Renderer.
package modstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

const (
	probeTimeout = 3 * time.Second
	waitInterval = 15 * time.Second
	waitDeadline = 10 * time.Minute
)

// Info is the module-agnostic status snapshot the engine produces. Renderers
// turn it into module-specific text/JSON.
type Info struct {
	ModuleStatus string
	Version      string
	InstanceID   string
	SGID         string

	InstanceType  string
	PrivateIP     string
	InstanceState string

	// ProbeAttempted is true when a TCP probe was made (only when PrivateIP is
	// known); Reachable is its result.
	ProbeAttempted bool
	Reachable      bool

	// LastBackupId / LastBackupAt are optional Perforce backup cache fields
	// from instance resource Properties (empty for other modules).
	LastBackupId string
	LastBackupAt string
}

// Renderer turns an Info into module-specific output. NotProvisioned handles the
// no-state case (text and JSON variants). Result renders a populated Info.
type Renderer interface {
	NotProvisioned(out io.Writer, jsonOut bool)
	Result(out io.Writer, info Info, jsonOut bool)
}

// Spec carries the per-module knobs that vary between status commands.
type Spec struct {
	ModuleName string
	// ProbePort is the TCP port probed for readiness (1666 perforce, 5000 horde).
	ProbePort int
	// Timeout label for the poll-timeout message (e.g. "Perforce", "Horde").
	DisplayName string
}

// Command runs a module status query. Func fields are seams the cmd layer wires
// to real implementations and tests replace with fakes.
type Command struct {
	Spec     Spec
	Renderer Renderer
	Runtime  globals.Runtime

	JSONOut bool
	Wait    bool
	Out     io.Writer

	ReadState   func() (*fabricastate.State, error)
	WriteState  func(*fabricastate.State) error
	GetResource func(ctx context.Context, r *cloud.Resource) error
	ProbeTCP    func(address string) bool
	Sleep       func(d time.Duration)
	Now         func() time.Time
}

// Run reads state, builds the status snapshot, transitions provisioning→ready
// when the probe first succeeds, and renders. With Wait set, it polls instead.
func (c Command) Run(ctx context.Context) error {
	st, err := c.ReadState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(c.Spec.ModuleName)
	if m == nil {
		c.Renderer.NotProvisioned(c.Out, c.JSONOut)
		return nil
	}

	if c.Wait {
		return c.pollUntilReady(ctx, st, m)
	}

	info, err := c.buildInfo(ctx, m)
	if err != nil {
		return err
	}

	if info.ProbeAttempted && info.Reachable && m.Status != "ready" {
		c.markReady(st, m, &info)
	}

	c.Renderer.Result(c.Out, info, c.JSONOut)
	return nil
}

func (c Command) pollUntilReady(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState) error {
	deadline := c.Now().Add(waitDeadline)
	for {
		info, err := c.buildInfo(ctx, m)
		if err != nil {
			return err
		}

		if info.ProbeAttempted && info.Reachable {
			c.markReady(st, m, &info)
			c.Renderer.Result(c.Out, info, c.JSONOut)
			return nil
		}

		c.Renderer.Result(c.Out, info, c.JSONOut)

		if c.Now().After(deadline) {
			fmt.Fprintf(c.Out, "Timed out waiting for %s to become ready (10 minutes).\n", c.Spec.DisplayName)
			return nil
		}

		fmt.Fprintf(c.Out, "Waiting %s before next check...\n\n", waitInterval)
		c.Sleep(waitInterval)
	}
}

// markReady transitions the module to "ready" in state and persists it,
// surfacing a write failure as a warning.
func (c Command) markReady(st *fabricastate.State, m *fabricastate.ModuleState, info *Info) {
	m.Status = "ready"
	info.ModuleStatus = "ready"
	st.UpsertModule(c.Spec.ModuleName, m.Version, "ready", m.Resources)
	if err := c.WriteState(st); err != nil {
		fmt.Fprintf(c.Out, "Warning: could not update local state: %v\n", err)
	}
}

// buildInfo queries Cloud Control and probes TCP to produce an Info.
func (c Command) buildInfo(ctx context.Context, m *fabricastate.ModuleState) (Info, error) {
	info := Info{
		ModuleStatus: m.Status,
		Version:      m.Version,
	}

	if sgRes, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
		info.SGID = sgRes.Identifier
	}

	instRes, hasInst := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !hasInst {
		return info, nil
	}
	info.InstanceID = instRes.Identifier
	if instRes.Properties != nil {
		info.LastBackupId = instRes.Properties["lastBackupId"]
		info.LastBackupAt = instRes.Properties["lastBackupAt"]
	}

	if c.GetResource != nil && info.InstanceID != "" {
		r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: info.InstanceID}
		if err := c.GetResource(ctx, r); err != nil {
			return info, fmt.Errorf("querying instance %s via Cloud Control: %w", info.InstanceID, err)
		}
		parseInstanceActualState(r, &info)
	}

	if info.PrivateIP != "" {
		info.ProbeAttempted = true
		info.Reachable = c.ProbeTCP(fmt.Sprintf("%s:%d", info.PrivateIP, c.Spec.ProbePort))
	}

	return info, nil
}

// parseInstanceActualState extracts fields from the Cloud Control ActualState
// JSON. Silently ignores unparseable or nil ActualState (Cloud Control may be stubbed).
func parseInstanceActualState(r *cloud.Resource, info *Info) {
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
	info.InstanceType = actual.InstanceType
	info.PrivateIP = actual.PrivateIPAddress
	info.InstanceState = actual.State.Name
}

// DefaultProbeTCP dials the address with the standard probe timeout.
func DefaultProbeTCP(address string) bool {
	conn, err := net.DialTimeout("tcp", address, probeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close() // best-effort; probe result already decided
	return true
}

// ProbeHTTP performs a GET request against the given address+path with a 3s timeout.
// Returns true when the response status is 200.
func ProbeHTTP(address, path string) bool {
	url := "http://" + address + path
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// WriteCommonFields writes the shared InstanceID, InstanceType, and PrivateIP
// lines. Call this from each module's Result renderer instead of duplicating.
func WriteCommonFields(out io.Writer, info Info) {
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
	}
}

// WriteNotProvisionedJSON writes the standard not-provisioned JSON output.
// Call this from the NotProvisioned renderer when jsonOut is true.
func WriteNotProvisionedJSON(out io.Writer) {
	data, _ := json.Marshal(map[string]interface{}{
		"provisioned": false,
		"status":      "not_provisioned",
	})
	fmt.Fprintln(out, string(data))
}

// WriteNotProvisionedText writes the standard not-provisioned text output.
// moduleName is the display name (e.g. "Perforce", "Horde"),
// createCmd is the command to run (e.g. "fabrica perforce create").
func WriteNotProvisionedText(out io.Writer, moduleName, createCmd string) {
	fmt.Fprintf(out, "%s is not provisioned. Run '%s' to set it up.\n", moduleName, createCmd)
}

// WriteJSON marshals v as indented JSON and writes it to out.
// Call this from each module's printJSON instead of duplicating the marshal+print.
func WriteJSON(out io.Writer, v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(out, string(data))
}

// WriteSecurityGroup writes the SG ID line if present.
func WriteSecurityGroup(out io.Writer, sgID string) {
	if sgID != "" {
		fmt.Fprintf(out, "  Security Group: %s\n", sgID)
	}
}

// WriteProbeStatusText renders the probe reachability status in text form.
// label is the module-specific label (e.g. "Helix Core", "Horde", "Lore", "DDC").
// version is optional — if non-empty it is appended to the responding line.
func WriteProbeStatusText(out io.Writer, info Info, label, version string) {
	if info.ProbeAttempted {
		if info.Reachable {
			if version != "" {
				fmt.Fprintf(out, "  %s:    %s (responding)\n", label, version)
				return
			}
			fmt.Fprintln(out, "  "+label+":    responding")
			return
		}
		fmt.Fprintln(out, "  "+label+":    unreachable from this machine (check VPN/network)")
		return
	}
	if info.ModuleStatus == "provisioning" {
		fmt.Fprintln(out, "  "+label+":    setting up... (~3 min from launch)")
	}
}

// ProbeStatus returns the probe status string for JSON output.
// Returns "responding", "unreachable", "setting up", or "" (no probe info).
func ProbeStatus(info Info) string {
	if info.ProbeAttempted {
		if info.Reachable {
			return "responding"
		}
		return "unreachable"
	}
	if info.ModuleStatus == "provisioning" {
		return "setting up"
	}
	return ""
}

// BaseStatusOutput contains the common fields shared by all EC2-instance
// StatusOutput structs. Embed this in module-specific output types.
type BaseStatusOutput struct {
	Provisioned  bool   `json:"provisioned"`
	Status       string `json:"status"`
	InstanceID   string `json:"instanceId,omitempty"`
	SGID         string `json:"sgId,omitempty"`
	InstanceType string `json:"instanceType,omitempty"`
	PrivateIP    string `json:"privateIp,omitempty"`
}

// NewBaseStatusOutput creates a BaseStatusOutput from an Info.
func NewBaseStatusOutput(info Info) BaseStatusOutput {
	return BaseStatusOutput{
		Provisioned:  true,
		Status:       info.ModuleStatus,
		InstanceID:   info.InstanceID,
		SGID:         info.SGID,
		InstanceType: info.InstanceType,
		PrivateIP:    info.PrivateIP,
	}
}

// FillFromInfo populates the common fields from an Info.
// Useful when the struct is not using embedding.
func (b *BaseStatusOutput) FillFromInfo(info Info) {
	b.Provisioned = true
	b.Status = info.ModuleStatus
	b.InstanceID = info.InstanceID
	b.SGID = info.SGID
	b.InstanceType = info.InstanceType
	b.PrivateIP = info.PrivateIP
}
