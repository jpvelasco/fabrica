package create

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName  = "horde"
	passwordLen = 24
)

type command struct {
	runtime      globals.Runtime
	dryRun       bool
	assumeYes    bool
	amiID        string
	instanceType string
	minSize      int
	desiredCap   int
	maxSize      int
	out          io.Writer
	costs        *fabricacost.Registry
	confirm      func(string, string) bool

	// seams for testing
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	deleteResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
}

// New returns the "horde agents create" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var amiID string
	var instanceType string
	var minSize int
	var desiredCap int
	var maxSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a Horde build agent pool",
		Long: `Provision a pool of Horde build agents on AWS.

Creates an Auto Scaling Group with a Launch Template that launches agent
instances. The agents enroll against the existing Horde coordinator using
its private IP address.

Creates five resources in order:
  1. Security Group — no inbound from internet; coordinator SG allowed
  2. IAM Role — AmazonSSMManagedInstanceCore for SSM access
  3. IAM Instance Profile — attaches the role to agent instances
  4. Launch Template — agent AMI, instance type, user data, profile, SG
  5. Auto Scaling Group — min/desired/max capacity, subnets, LT

Prerequisites:
  - 'fabrica horde create' must have been run first (coordinator required)
  - horde.agents.amiId must be set in fabrica.yaml or passed via --ami-id

With --dry-run, shows the provisioning plan and a monthly cost estimate
without making any AWS calls.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := command{
				runtime:      rt,
				dryRun:       opts.DryRun,
				assumeYes:    opts.AssumeYes,
				amiID:        amiID,
				instanceType: instanceType,
				minSize:      minSize,
				desiredCap:   desiredCap,
				maxSize:      maxSize,
				out:          out,
				costs:        fabricacost.Global,
				confirm:      prompt.ConfirmExact,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
				c.deleteResource = rt.Provider.Resources().Delete
				c.getResource = rt.Provider.Resources().Get
			}
			return c.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&amiID, "ami-id", "", "Agent AMI ID (required, overrides horde.agents.amiId)")
	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: c7i.xlarge)")
	cmd.Flags().IntVar(&minSize, "min-size", 0, "Minimum ASG size (default: 0)")
	cmd.Flags().IntVar(&desiredCap, "desired-capacity", 0, "Desired ASG capacity (default: 1)")
	cmd.Flags().IntVar(&maxSize, "max-size", 0, "Maximum ASG size (default: 2)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
	}

	agentsCfg := c.runtime.Config.Horde.Agents
	if c.amiID != "" {
		agentsCfg.AmiID = c.amiID
	}
	if c.instanceType != "" {
		agentsCfg.InstanceType = c.instanceType
	}
	if c.minSize >= 0 && c.desiredCap > 0 {
		agentsCfg.DesiredCapacity = c.desiredCap
	}
	if c.maxSize > 0 {
		agentsCfg.MaxSize = c.maxSize
	}
	if c.minSize > 0 {
		agentsCfg.MinSize = c.minSize
	}

	// Resolve coordinator details from state + Cloud Control.
	coordIP, coordPort, coordSGID, err := c.resolveCoordinator(ctx)
	if err != nil {
		return err
	}

	var resolver cloud.VPCResolver
	if vr, ok := c.runtime.Provider.(cloud.VPCResolver); ok {
		resolver = vr
	}

	plan, err := horde.NewAgentsCreatePlan(ctx, agentsCfg, coordIP, coordPort, coordSGID, account, region, resolver)
	if err != nil {
		return fmt.Errorf("building agents create plan: %w", err)
	}

	// Check if agents are already provisioned.
	if !c.dryRun {
		st, err := c.readState()
		if err != nil {
			return fmt.Errorf("reading state: %w", err)
		}
		if agentsProvisioned(st) {
			fmt.Fprintln(c.out, "Horde agents are already provisioned. Run 'fabrica horde agents status' to check health.")
			fmt.Fprintln(c.out, "Use 'fabrica horde agents destroy' to remove them first.")
			return nil
		}
	}

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	c.printApplyPlan(plan)
	if !provision.ConfirmCreate(c.out, moduleName, account, c.assumeYes, c.confirm) {
		return nil
	}

	return c.applyCreate(ctx, st, plan)
}

func (c command) resolveCoordinator(ctx context.Context) (ip string, port int, sgID string, err error) {
	st, err := c.readState()
	if err != nil {
		return "", 0, "", fmt.Errorf("reading state for coordinator: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		return "", 0, "", fmt.Errorf("Horde coordinator is not provisioned. Run 'fabrica horde create' first before creating agents")
	}

	// Get coordinator instance ID from state.
	instRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
	if !ok || instRes.Identifier == "" {
		return "", 0, "", fmt.Errorf("coordinator instance not found in state. Run 'fabrica horde create' first")
	}

	// Get coordinator SG from state.
	sgRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2SecurityGroup)
	if ok && sgRes.Identifier != "" {
		sgID = sgRes.Identifier
	}

	// Resolve private IP from Cloud Control.
	if c.getResource == nil {
		return "", 0, "", fmt.Errorf("no provider available to resolve coordinator IP")
	}

	cloudRes := &cloud.Resource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Identifier: instRes.Identifier,
	}
	if err := c.getResource(ctx, cloudRes); err != nil {
		return "", 0, "", fmt.Errorf("querying coordinator instance %s: %w", instRes.Identifier, err)
	}

	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if len(cloudRes.ActualState) == 0 {
		return "", 0, "", fmt.Errorf("coordinator instance %s has no state data; try again shortly", instRes.Identifier)
	}
	if err := json.Unmarshal(cloudRes.ActualState, &actual); err != nil || actual.PrivateIPAddress == "" {
		return "", 0, "", fmt.Errorf("could not determine coordinator private IP for instance %s", instRes.Identifier)
	}

	// Resolve port from config.
	port = c.runtime.Config.Horde.Port
	if port <= 0 {
		port = 5000
	}

	return actual.PrivateIPAddress, port, sgID, nil
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *horde.AgentsCreatePlan) error {
	// Seed with existing module resources (coordinator) so UpsertModule
	// preserves them alongside the new agent resources.
	m := st.GetModule(moduleName)
	resources := m.Resources
	// Preserve the coordinator AMI as the module version — agents are
	// tracked via Properties["role"] = "agent", not a separate version.
	ver := m.Version

	// 1. Security Group
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating agent security group %s...\n", plan.SGName)
	resources, err := provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Agent security group",
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) {
			return horde.AgentSGDesiredState(plan)
		},
		Properties: map[string]string{"role": "agent"},
	}, moduleName, ver, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating agent security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

	// 2. IAM Role
	fmt.Fprintf(c.out, "Creating agent IAM role %s...\n", plan.RoleName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Agent IAM role",
		TypeName:          cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) { return horde.AgentRoleDesiredState(plan) },
		Properties:        map[string]string{"role": "agent"},
	}, moduleName, ver, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating agent IAM role: %w", err)
	}

	// 3. Instance Profile
	fmt.Fprintf(c.out, "Creating agent instance profile %s...\n", plan.InstanceProfileName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Agent instance profile",
		TypeName:          cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) { return horde.AgentInstanceProfileDesiredState(plan) },
		ResourceIdentifier: func(created *cloud.Resource) string {
			name := plan.InstanceProfileName
			if created.Identifier != "" && !strings.HasPrefix(created.Identifier, "arn:") {
				name = created.Identifier
			}
			return name
		},
		Properties: map[string]string{"role": "agent"},
	}, moduleName, ver, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating agent instance profile: %w", err)
	}

	// 4. Launch Template
	fmt.Fprintf(c.out, "Creating launch template %s...\n", plan.LaunchTemplateName)
	userData, err := horde.AgentUserData(horde.AgentUserDataConfig{
		CoordinatorIP:   plan.CoordinatorPrivateIP,
		CoordinatorPort: plan.CoordinatorPort,
	})
	if err != nil {
		return fmt.Errorf("generating agent user data: %w", err)
	}

	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Launch template",
		TypeName: cloud.TypeAWSEC2LaunchTemplate,
		BuildDesiredState: func() ([]byte, error) {
			return horde.LaunchTemplateDesiredState(plan, sgID, userData)
		},
		Properties: map[string]string{
			"role":         "agent",
			"instanceType": plan.InstanceType,
			"imageId":      plan.AmiID,
		},
	}, moduleName, ver, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating launch template: %w", err)
	}
	ltID := resources[len(resources)-1].Identifier

	// 5. Auto Scaling Group
	fmt.Fprintf(c.out, "Creating auto scaling group %s...\n", plan.ASGName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Auto Scaling Group",
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		BuildDesiredState: func() ([]byte, error) {
			return horde.ASGDesiredState(plan, ltID)
		},
		Properties: map[string]string{
			"role":            "agent",
			"minSize":         strconv.Itoa(plan.MinSize),
			"desiredCapacity": strconv.Itoa(plan.DesiredCapacity),
			"maxSize":         strconv.Itoa(plan.MaxSize),
			"instanceType":    plan.InstanceType,
			"imageId":         plan.AmiID,
		},
	}, moduleName, ver, "ready", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating auto scaling group: %w", err)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

func (c command) printDryRun(plan *horde.AgentsCreatePlan) {
	provision.DryRun(c.out, provision.DryRunSpec{
		Title: "Horde build agent pool",
		Info: provision.PlanInfo{
			Account:      plan.Account,
			Region:       plan.Region,
			InstanceType: plan.InstanceType,
			VolumeSize:   0,
			VPCID:        plan.VPCID,
			DefaultVPC:   plan.DefaultVPC,
		},
		ExtraFields: []provision.PlanField{
			{Key: "Agent AMI ID", Value: plan.AmiID},
			{Key: "Coordinator IP", Value: plan.CoordinatorPrivateIP},
			{Key: "Coordinator port", Value: fmt.Sprintf("%d", plan.CoordinatorPort)},
			{Key: "Min size", Value: fmt.Sprintf("%d", plan.MinSize)},
			{Key: "Desired capacity", Value: fmt.Sprintf("%d", plan.DesiredCapacity)},
			{Key: "Max size", Value: fmt.Sprintf("%d", plan.MaxSize)},
		},
		Resources: []string{
			"Agent Security Group:   " + plan.SGName,
			"Agent IAM Role:         " + plan.RoleName,
			"Agent Instance Profile: " + plan.InstanceProfileName,
			"Launch Template:        " + plan.LaunchTemplateName,
			"Auto Scaling Group:     " + plan.ASGName,
		},
		CostResources: plan.CostResources,
		Costs:         c.costs,
	})
}

func (c command) printApplyPlan(plan *horde.AgentsCreatePlan) {
	provision.ApplyPlan(c.out, "Horde build agent pool", provision.PlanInfo{
		Account:      plan.Account,
		Region:       plan.Region,
		InstanceType: plan.InstanceType,
		VolumeSize:   0,
	}, []provision.PlanField{
		{Key: "Agent AMI ID", Value: plan.AmiID},
		{Key: "Coordinator", Value: fmt.Sprintf("%s:%d", plan.CoordinatorPrivateIP, plan.CoordinatorPort)},
		{Key: "Capacity", Value: fmt.Sprintf("%d/%d/%d (min/desired/max)", plan.MinSize, plan.DesiredCapacity, plan.MaxSize)},
	}, []string{
		"Agent Security Group:   " + plan.SGName,
		"Agent IAM Role:         " + plan.RoleName,
		"Agent Instance Profile: " + plan.InstanceProfileName,
		"Launch Template:        " + plan.LaunchTemplateName,
		"Auto Scaling Group:     " + plan.ASGName,
	})
}

func (c command) printPostCreate(plan *horde.AgentsCreatePlan, asgID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Horde agent pool",
		InstanceID:   asgID,
		StatusDetail: fmt.Sprintf("provisioning (%d desired instances launching)", plan.DesiredCapacity),
		Details: []provision.PlanField{
			{Key: "Coordinator", Value: fmt.Sprintf("%s:%d", plan.CoordinatorPrivateIP, plan.CoordinatorPort)},
			{Key: "Capacity", Value: fmt.Sprintf("%d/%d/%d (min/desired/max)", plan.MinSize, plan.DesiredCapacity, plan.MaxSize)},
			{Key: "Launch Template", Value: plan.LaunchTemplateName},
		},
		NextSteps: []string{
			"fabrica horde agents status    Check agent pool status",
			"fabrica horde status           Check coordinator health",
		},
		RawAfter: func(w io.Writer) {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  Note: Agents are launched in private subnets. Operator access")
			fmt.Fprintln(w, "        is via AWS Systems Manager Session Manager.")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  If agents don't enroll within 5 minutes, check:")
			fmt.Fprintln(w, "    /var/log/fabrica-horde-agent-init.log  on each agent instance")
		},
	})
}

// agentsProvisioned returns true if the horde module has an ASG resource recorded.
func agentsProvisioned(st *fabricastate.State) bool {
	m := st.GetModule(moduleName)
	if m == nil {
		return false
	}
	_, ok := stateutil.ResourceByType(m, cloud.TypeAWSAutoScalingAutoScalingGroup)
	return ok
}
