package create

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
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
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
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
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
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

	return provision.RunCreate(ctx, provision.CreateSpec[*workstation.CreatePlan]{
		ModuleName:      moduleName,
		Account:         account,
		Plan:            plan,
		DryRun:          c.dryRun,
		AssumeYes:       c.assumeYes,
		Out:             c.out,
		ExistingMessage: "Workstation is already provisioned. Run 'fabrica workstation list' to view details.\n",
		Confirm:         c.confirm,
		ReadState:       c.readState,
		PrintDryRun:     c.printDryRun,
		PrintApplyPlan:  c.printApplyPlan,
		Apply:           c.applyCreate,
	})
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

	credContent := credentials.FormatWorkstation(sessionPass)
	if err := credentials.WriteCredentials(credFile, credContent); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nDCV credentials written to %s\n", credFile)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	var resources []fabricastate.ModuleResource
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Security group",
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) {
			return workstation.SGDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

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

	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return workstation.InstanceDesiredState(plan, sgID, userData)
		},
		Properties: map[string]string{
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
		// fail on writeState error (default)
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating EC2 instance: %w", err)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

func (c command) printDryRun(plan *workstation.CreatePlan) {
	extraFields := []provision.PlanField{
		{Key: "AMI ID", Value: plan.AmiID},
		{Key: "Idle timeout", Value: fmt.Sprintf("%d min", plan.IdleTimeoutMinutes)},
	}
	if plan.MountPerforce {
		extraFields = append(extraFields, provision.PlanField{Key: "Perforce server", Value: plan.PerforceServerAddr})
	}
	provision.DryRun(c.out, provision.DryRunSpec{
		Title: "Cloud Workstation",
		Info: provision.PlanInfo{
			Account:      plan.Account,
			Region:       plan.Region,
			InstanceType: plan.InstanceType,
			VolumeSize:   plan.VolumeSize,
			AllowedCIDR:  plan.AllowedCIDR,
			VPCID:        plan.VPCID,
			DefaultVPC:   plan.DefaultVPC,
		},
		ExtraFields: extraFields,
		Resources: []string{
			"Security Group:   " + plan.SGName,
			"EC2 Instance:     " + plan.InstanceName,
		},
		CostResources: plan.CostResources,
		Costs:         c.costs,
		CidrWarning:   "port 8443 is open to the internet. Set workstation.allowedCidr in fabrica.yaml before deploying to production.",
	})
}

func (c command) printApplyPlan(plan *workstation.CreatePlan) {
	// Workstation apply is intentionally terser than dry-run and other modules:
	// no Data volume line, compact labels, CIDR WARNING before the resource list
	// (pre-#162 layout). Shared WriteApplyPlan owns formatting; opts preserve
	// that product choice (see PR #162 review discussion_r3670766352).
	extraFields := []provision.PlanField{}
	if plan.MountPerforce {
		extraFields = append(extraFields, provision.PlanField{Key: "Perforce", Value: plan.PerforceServerAddr})
	}
	provision.WriteApplyPlan(c.out, provision.ApplyPlanSpec{
		Title: "Cloud Workstation",
		Info: provision.PlanInfo{
			Account:      plan.Account,
			Region:       plan.Region,
			InstanceType: plan.InstanceType,
		},
		ExtraFields:   extraFields,
		OmitVolume:    true,
		CompactLabels: true,
		Resources: []string{
			// Compact resource labels match pre-#162 workstation apply.
			"Security Group: " + plan.SGName,
			"EC2 Instance:   " + plan.InstanceName,
		},
		BeforeResources: func(w io.Writer) {
			if plan.AllowedCIDR != "0.0.0.0/0" {
				return
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  WARNING: allowedCidr is 0.0.0.0/0 — port 8443 is open to")
			fmt.Fprintln(w, "           the internet. Set workstation.allowedCidr in fabrica.yaml")
			fmt.Fprintln(w, "           before deploying to production.")
		},
	})
}

func (c command) printPostCreate(_ *workstation.CreatePlan, instanceID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Cloud Workstation",
		InstanceID:   instanceID,
		StatusDetail: "provisioning (DCV setup in progress)",
		Details: []provision.PlanField{
			{Key: "DCV credentials", Value: credFile},
		},
		NextSteps: []string{
			"fabrica workstation list     Show workstation details",
		},
	})
}
