package create

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/jpvelasco/fabrica/internal/workstation"
	"github.com/spf13/cobra"
)

const (
	lineWidth   = 58
	moduleName  = "workstation"
	credFile    = ".fabrica/workstation-credentials.yaml"
	passwordLen = 24
)

type command struct {
	runtime       globals.Runtime
	dryRun        bool
	assumeYes     bool
	instanceType  string
	volumeSize    int
	template      string
	mountPerforce bool
	out           io.Writer
	costs         *fabricacost.Registry
	confirm       func(string, string) bool

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType string
	var volumeSize int
	var template string
	var mountPerforce bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a cloud workstation",
		Long: `Provision a NICE DCV cloud workstation on AWS.

Creates two resources in order:
  1. EC2 Security Group — allows TCP 8443 inbound (NICE DCV HTTPS)
  2. EC2 Instance — runs NICE DCV from the provided AMI

State is written after each resource so a partial failure is recoverable:
re-running create will detect the already-provisioned module and exit cleanly.

A random DCV session password is written to .fabrica/workstation-credentials.yaml.

Use --template to set sensible defaults for common workstation roles:
  artist      g6.xlarge (NVIDIA L4 GPU), 200 GiB
  programmer  c7i.xlarge (Intel Sapphire Rapids), 100 GiB

Use --mount-perforce to install the Perforce CLI and write a ~/.p4config
pointing at the provisioned Perforce server (reads server IP from local state).

With --dry-run, shows the provisioning plan and a monthly cost estimate without
making any AWS calls.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := command{
				runtime:       rt,
				dryRun:        opts.DryRun,
				assumeYes:     opts.AssumeYes,
				instanceType:  instanceType,
				volumeSize:    volumeSize,
				template:      template,
				mountPerforce: mountPerforce,
				out:           out,
				costs:         fabricacost.Global,
				confirm:       prompt.ConfirmExact,
			}
			c.readState = c.defaultReadState
			c.writeState = c.defaultWriteState
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
				c.getResource = rt.Provider.Resources().Get
			}
			return c.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: g4dn.xlarge)")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "EBS root volume size in GiB (default: 100)")
	cmd.Flags().StringVar(&template, "template", "", `Workstation profile: "artist" (g6.xlarge, 200 GiB) or "programmer" (c7i.xlarge, 100 GiB)`)
	cmd.Flags().BoolVar(&mountPerforce, "mount-perforce", false, "Install Perforce CLI and configure ~/.p4config from local Fabrica state")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	wsCfg := c.runtime.Config.Workstation
	if c.instanceType != "" {
		wsCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		wsCfg.VolumeSize = c.volumeSize
	}

	// Resolve Perforce server address from local state when --mount-perforce is set.
	perforceAddr := ""
	if c.mountPerforce {
		addr, err := c.resolvePerforceAddr(ctx)
		if err != nil {
			return err
		}
		perforceAddr = addr
	}

	plan, err := workstation.NewCreatePlan(ctx, wsCfg, account, region, nil, c.template, perforceAddr)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if m := st.GetModule(moduleName); m != nil {
		fmt.Fprintf(c.out, "Workstation is already provisioned. Run 'fabrica workstation list' to view details.\n")
		return nil
	}

	c.printApplyPlan(plan)

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := fmt.Sprintf("create workstation %s", account)
		c.printConfirmInstructions(phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.applyCreate(ctx, st, plan)
}

// resolvePerforceAddr reads the Perforce module state and resolves the instance's
// private IP via Cloud Control. Returns an error when the Perforce module is not found.
func (c command) resolvePerforceAddr(ctx context.Context) (string, error) {
	st, err := c.readState()
	if err != nil {
		return "", fmt.Errorf("reading state for Perforce address: %w", err)
	}
	m := st.GetModule("perforce")
	if m == nil {
		return "", fmt.Errorf("--mount-perforce requires a provisioned Perforce server. Run 'fabrica perforce create' first.")
	}
	instRes, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || instRes.Identifier == "" {
		return "", fmt.Errorf("Perforce instance not found in state. Run 'fabrica perforce status' to confirm readiness.")
	}
	if c.getResource == nil {
		return "", fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instRes.Identifier}
	if err := c.getResource(ctx, r); err != nil {
		return "", fmt.Errorf("querying Perforce instance %s: %w", instRes.Identifier, err)
	}
	if len(r.ActualState) == 0 {
		return "", fmt.Errorf("Perforce instance %s has no state data; try again shortly", instRes.Identifier)
	}
	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil || actual.PrivateIPAddress == "" {
		return "", fmt.Errorf("could not determine Perforce private IP for instance %s", instRes.Identifier)
	}
	return fmt.Sprintf("%s:1666", actual.PrivateIPAddress), nil
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *workstation.CreatePlan) error {
	sessionPass, err := credentials.GeneratePassword(passwordLen)
	if err != nil {
		return fmt.Errorf("generating session password: %w", err)
	}

	credContent := fmt.Sprintf("# Workstation DCV credentials — keep secret\ndcv_session_password: %q\n", sessionPass)
	if err := credentials.WriteCredentials(credFile, credContent); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nDCV credentials written to %s\n", credFile)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	sgDesired, err := workstation.SGDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building SG desired state: %w", err)
	}
	sg := &cloud.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: sgDesired}
	if err := c.createResource(ctx, sg); err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	fmt.Fprintf(c.out, "  Security group created: %s\n", sg.Identifier)

	st.UpsertModule(moduleName, plan.AmiID, "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after SG creation: %w", err)
	}

	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)

	userData, err := workstation.Generate(workstation.UserDataConfig{
		SessionPassword:    sessionPass,
		IdleTimeoutMinutes: plan.IdleTimeoutMinutes,
		MountPerforce:      plan.MountPerforce,
		PerforceServerAddr: plan.PerforceServerAddr,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	instanceDesired, err := workstation.InstanceDesiredState(plan, sg.Identifier, userData)
	if err != nil {
		return fmt.Errorf("building instance desired state: %w", err)
	}
	instance := &cloud.Resource{TypeName: "AWS::EC2::Instance", DesiredState: instanceDesired}
	if err := c.createResource(ctx, instance); err != nil {
		return fmt.Errorf("creating EC2 instance: %w", err)
	}
	fmt.Fprintf(c.out, "  Instance created: %s\n", instance.Identifier)

	st.UpsertModule(moduleName, plan.AmiID, "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
		{TypeName: "AWS::EC2::Instance", Identifier: instance.Identifier},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after instance creation: %w", err)
	}

	c.printPostCreate(plan, instance.Identifier)
	return nil
}

func (c command) printDryRun(plan *workstation.CreatePlan) {
	fmt.Fprintln(c.out, "Cloud Workstation (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Volume:           %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintf(c.out, "  Idle timeout:     %d min\n", plan.IdleTimeoutMinutes)
	if plan.DefaultVPC {
		fmt.Fprintf(c.out, "  VPC:              default (%s)\n", plan.VPCID)
	} else if plan.VPCID != "" {
		fmt.Fprintf(c.out, "  VPC:              %s\n", plan.VPCID)
	}
	if plan.MountPerforce {
		fmt.Fprintf(c.out, "  Perforce server:  %s\n", plan.PerforceServerAddr)
	}
	if plan.AllowedCIDR == "0.0.0.0/0" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  WARNING: allowedCidr is 0.0.0.0/0 — port 8443 is open to")
		fmt.Fprintln(c.out, "           the internet. Set workstation.allowedCidr in fabrica.yaml")
		fmt.Fprintln(c.out, "           before deploying to production.")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
	fmt.Fprintln(c.out)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printApplyPlan(plan *workstation.CreatePlan) {
	fmt.Fprintln(c.out, "Cloud Workstation")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:   %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:    %s\n", plan.Region)
	fmt.Fprintf(c.out, "  Instance type: %s\n", plan.InstanceType)
	if plan.MountPerforce {
		fmt.Fprintf(c.out, "  Perforce:      %s\n", plan.PerforceServerAddr)
	}
	if plan.AllowedCIDR == "0.0.0.0/0" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  WARNING: allowedCidr is 0.0.0.0/0 — port 8443 is open to")
		fmt.Fprintln(c.out, "           the internet. Set workstation.allowedCidr in fabrica.yaml")
		fmt.Fprintln(c.out, "           before deploying to production.")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group: %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:   %s\n", plan.InstanceName)
}

func (c command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.out, "Confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", phrase)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels.")
}

func (c command) printPostCreate(_ *workstation.CreatePlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Cloud Workstation provisioned.")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Instance ID:   %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:        provisioning (DCV setup in progress)\n")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  DCV credentials: %s\n", credFile)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica workstation list     Show workstation details")
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
