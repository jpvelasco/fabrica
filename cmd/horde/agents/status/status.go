// Package status provides the "horde agents status" subcommand.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName  = "horde"
	lineWidth   = 58
	defaultPort = 5000
)

// StatusOutput is the JSON-serialisable view of the agent pool status.
type StatusOutput struct {
	Provisioned     bool   `json:"provisioned"`
	ASGID           string `json:"asgId,omitempty"`
	LaunchTemplate  string `json:"launchTemplate,omitempty"`
	MinSize         int    `json:"minSize"`
	DesiredCapacity int    `json:"desiredCapacity"`
	MaxSize         int    `json:"maxSize"`
	InstanceType    string `json:"instanceType,omitempty"`
	AmiID           string `json:"amiId,omitempty"`
	CoordinatorIP   string `json:"coordinatorIp,omitempty"`
	CoordinatorPort int    `json:"coordinatorPort,omitempty"`
	Status          string `json:"status,omitempty"`
	// Live fields from ASG SDK query (not Cloud Control — lifecycle data
	// is only available via the Auto Scaling SDK). Zero values are meaningful
	// (e.g., scaled to 0) so omitempty is not used.
	LiveDesiredCapacity int    `json:"liveDesiredCapacity"`
	LiveMinSize         int    `json:"liveMinSize"`
	LiveMaxSize         int    `json:"liveMaxSize"`
	InService           int    `json:"inService"`
	Pending             int    `json:"pending"`
	Terminating         int    `json:"terminating"`
	ASGHealth           string `json:"asgHealth,omitempty"`
	// Scaling policy fields — populated when scaling resources exist in state.
	ScalingEnabled    bool   `json:"scalingEnabled"`
	ScaleOutThreshold string `json:"scaleOutThreshold,omitempty"`
	ScaleInThreshold  string `json:"scaleInThreshold,omitempty"`
	ScaleInCooldown   string `json:"scaleInCooldown,omitempty"`
	MetricName        string `json:"metricName,omitempty"`
	MetricNamespace   string `json:"metricNamespace,omitempty"`
	ScaleOutAlarmID   string `json:"scaleOutAlarmId,omitempty"`
	ScaleInAlarmID    string `json:"scaleInAlarmId,omitempty"`
	ScaleOutPolicyID  string `json:"scaleOutPolicyId,omitempty"`
	ScaleInPolicyID   string `json:"scaleInPolicyId,omitempty"`
}

type command struct {
	runtime     globals.Runtime
	dryRun      bool
	jsonOut     bool
	out         io.Writer
	readState   func() (*fabricastate.State, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
	// describeASG is the ASGManager seam — wired via type assertion on the
	// provider. Tests inject a fake; nil means live query is skipped.
	describeASG func(ctx context.Context, asgName string) (cloud.ASGInfo, error)
}

// New returns the "horde agents status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show Horde agent pool status",
		Long: `Show the current status of the Horde agent pool.

Reads local module state for the ASG, launch template, and capacity
settings. Queries the Auto Scaling API for live instance lifecycle data
(InService, Pending, Terminating) when a provider is available.

Use --json for machine-readable output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := command{
				runtime: rt,
				dryRun:  opts.DryRun,
				jsonOut: opts.JSONOutput,
				out:     out,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
				if asgMgr, ok := rt.Provider.(cloud.ASGManager); ok {
					c.describeASG = func(ctx context.Context, name string) (cloud.ASGInfo, error) {
						return asgMgr.DescribeASG(ctx, name)
					}
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c *command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		c.printNotProvisioned()
		return nil
	}

	// Check for ASG resource.
	asgRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSAutoScalingAutoScalingGroup)
	if !ok || asgRes.Identifier == "" {
		c.printNotProvisioned()
		return nil
	}

	// Get launch template resource.
	ltRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2LaunchTemplate)
	if !ok {
		ltRes = fabricastate.ModuleResource{}
	}

	// Build status output from state.
	o := StatusOutput{
		Provisioned:     true,
		ASGID:           asgRes.Identifier,
		LaunchTemplate:  ltRes.Identifier,
		MinSize:         parseIntProp(asgRes.Properties, "minSize"),
		DesiredCapacity: parseIntProp(asgRes.Properties, "desiredCapacity"),
		MaxSize:         parseIntProp(asgRes.Properties, "maxSize"),
		InstanceType:    asgRes.Properties["instanceType"],
		AmiID:           asgRes.Properties["imageId"],
		Status:          m.Status,
	}

	// Resolve coordinator IP from coordinator instance.
	if c.getResource != nil {
		coordRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
		if ok && coordRes.Identifier != "" {
			cloudRes := &cloud.Resource{
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: coordRes.Identifier,
			}
			if err := c.getResource(ctx, cloudRes); err == nil {
				var actual struct {
					PrivateIPAddress string `json:"PrivateIpAddress"`
				}
				if len(cloudRes.ActualState) > 0 {
					_ = json.Unmarshal(cloudRes.ActualState, &actual)
					o.CoordinatorIP = actual.PrivateIPAddress
				}
			}
		}
	}

	// Query live ASG lifecycle data via the ASGManager SDK auxiliary interface.
	// Cloud Control cannot provide InService/Pending/Terminating counts.
	if c.describeASG != nil {
		asgName := asgRes.Identifier
		// ASG identifiers in state are the ASG name (Cloud Control returns
		// the name as the identifier). If it looks like an ARN or ID, use it
		// as-is; the SDK Describe call accepts the name.
		info, err := c.describeASG(ctx, asgName)
		if err == nil {
			o.LiveDesiredCapacity = info.DesiredCapacity
			o.LiveMinSize = info.MinSize
			o.LiveMaxSize = info.MaxSize
			o.InService = info.InService
			o.Pending = info.Pending
			o.Terminating = info.Terminating
			o.ASGHealth = formatASGHealth(info)
		}
	}

	o.CoordinatorPort = defaultPort
	if c.runtime.Config != nil && c.runtime.Config.Horde.Port > 0 {
		o.CoordinatorPort = c.runtime.Config.Horde.Port
	}

	// Populate scaling policy info from state.
	o.populateScaling(m)

	if c.jsonOut {
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		return enc.Encode(o)
	}

	c.printText(o)
	return nil
}

func (c *command) printText(o StatusOutput) {
	fmt.Fprintln(c.out, "Horde agent pool")
	fmt.Fprintln(c.out, lineWidthDash())
	fmt.Fprintf(c.out, "  Status:           %s\n", o.Status)
	fmt.Fprintf(c.out, "  ASG ID:           %s\n", o.ASGID)
	if o.LaunchTemplate != "" {
		fmt.Fprintf(c.out, "  Launch Template:    %s\n", o.LaunchTemplate)
	}
	fmt.Fprintf(c.out, "  Capacity:         %d/%d/%d (min/desired/max)\n", o.MinSize, o.DesiredCapacity, o.MaxSize)

	// Show live capacity whenever the ASG query succeeded (even if scaled to 0).
	if o.ASGHealth != "" {
		fmt.Fprintf(c.out, "  Live Capacity:    %d/%d/%d (min/desired/max)\n", o.LiveMinSize, o.LiveDesiredCapacity, o.LiveMaxSize)
		fmt.Fprintf(c.out, "  Health:           %s\n", o.ASGHealth)
		if o.Pending > 0 {
			fmt.Fprintf(c.out, "  Pending:          %d\n", o.Pending)
		}
		if o.Terminating > 0 {
			fmt.Fprintf(c.out, "  Terminating:    %d\n", o.Terminating)
		}
	}

	if o.InstanceType != "" {
		fmt.Fprintf(c.out, "  Instance Type:    %s\n", o.InstanceType)
	}
	if o.AmiID != "" {
		fmt.Fprintf(c.out, "  Agent AMI:        %s\n", o.AmiID)
	}
	if o.CoordinatorIP != "" {
		fmt.Fprintf(c.out, "  Coordinator:      %s:%d\n", o.CoordinatorIP, o.CoordinatorPort)
	}

	// Show scaling policy summary when enabled.
	if o.ScalingEnabled {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  Queue Scaling:")
		fmt.Fprintf(c.out, "    Enabled:          yes\n")
		fmt.Fprintf(c.out, "    Metric:           %s/%s\n", o.MetricNamespace, o.MetricName)
		fmt.Fprintf(c.out, "    Scale-out at:     %s\n", o.ScaleOutThreshold)
		fmt.Fprintf(c.out, "    Scale-in at:      %s\n", o.ScaleInThreshold)
		fmt.Fprintf(c.out, "    Cooldown:         %s\n", o.ScaleInCooldown)
		if o.ScaleOutPolicyID != "" {
			fmt.Fprintf(c.out, "    Scale-out policy: %s\n", o.ScaleOutPolicyID)
		}
		if o.ScaleInPolicyID != "" {
			fmt.Fprintf(c.out, "    Scale-in policy:  %s\n", o.ScaleInPolicyID)
		}
		if o.ScaleOutAlarmID != "" {
			fmt.Fprintf(c.out, "    Scale-out alarm:  %s\n", o.ScaleOutAlarmID)
		}
		if o.ScaleInAlarmID != "" {
			fmt.Fprintf(c.out, "    Scale-in alarm:   %s\n", o.ScaleInAlarmID)
		}
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "    Note: Queue scaling requires an external metric publisher.")
		fmt.Fprintf(c.out, "          Ensure agents publish the %s metric to CloudWatch.\n", o.MetricName)
	}
}

func (c *command) printNotProvisioned() {
	if c.jsonOut {
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(StatusOutput{Provisioned: false})
		return
	}
	fmt.Fprintln(c.out, "Horde agents are not provisioned. Run 'fabrica horde agents create' to provision the agent pool.")
}

func lineWidthDash() string {
	return strings.Repeat("-", lineWidth)
}

// formatASGHealth returns a human-readable health summary from live ASG data.
func formatASGHealth(info cloud.ASGInfo) string {
	if info.DesiredCapacity == 0 {
		return "scaled to 0"
	}
	total := info.InService + info.Pending + info.Terminating
	if info.InService == info.DesiredCapacity {
		return fmt.Sprintf("%d/%d InService", info.InService, info.DesiredCapacity)
	}
	if total > 0 {
		return fmt.Sprintf("%d/%d InService (%d pending, %d terminating)", info.InService, info.DesiredCapacity, info.Pending, info.Terminating)
	}
	return fmt.Sprintf("0/%d InService", info.DesiredCapacity)
}

func parseIntProp(props map[string]string, key string) int {
	if props == nil {
		return 0
	}
	v := props[key]
	var n int
	_, _ = fmt.Sscanf(v, "%d", &n)
	return n
}

// populateScaling reads scaling resources from module state and fills the
// corresponding fields in the status output.
func (o *StatusOutput) populateScaling(m *fabricastate.ModuleState) {
	if m == nil {
		return
	}

	for _, r := range m.Resources {
		if r.Properties == nil {
			continue
		}
		// Only look at agent-role resources.
		if r.Properties["role"] != "agent" {
			continue
		}

		switch r.TypeName {
		case cloud.TypeAWSAutoScalingScalingPolicy:
			o.ScalingEnabled = true
			policyType := r.Properties["scalingPolicy"]
			switch policyType {
			case "scale-out":
				o.ScaleOutPolicyID = r.Identifier
				o.ScaleOutThreshold = r.Properties["scaleOutThreshold"]
			case "scale-in":
				o.ScaleInPolicyID = r.Identifier
				o.ScaleInThreshold = r.Properties["scaleInThreshold"]
				o.ScaleInCooldown = r.Properties["cooldown"] + "s"
			}
		case cloud.TypeAWSCloudWatchAlarm:
			if r.Properties["scalingAlarm"] == "scale-out" {
				o.ScaleOutAlarmID = r.Identifier
				o.MetricName = r.Properties["metricName"]
				o.MetricNamespace = r.Properties["metricNs"]
			}
			if r.Properties["scalingAlarm"] == "scale-in" {
				o.ScaleInAlarmID = r.Identifier
			}
		}
	}
}
