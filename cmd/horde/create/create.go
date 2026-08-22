package create

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	moduleName  = "horde"
	credFile    = ".fabrica/horde-credentials.yaml" // #nosec G101 -- file path, not a credential
	passwordLen = 24
)

type command struct {
	runtime      globals.Runtime
	dryRun       bool
	assumeYes    bool
	instanceType string
	volumeSize   int
	out          io.Writer
	costs        *fabricacost.Registry
	confirm      func(string, string) bool

	// seams for testing
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	genPassword    func(int) (string, error)
	genUserData    func(horde.UserDataConfig) (string, error)
}

// New returns the "horde create" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType string
	var volumeSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision an Unreal Horde build coordinator",
		Long: `Provision an Unreal Horde build coordinator on AWS.

Creates four resources in order:
  1. EC2 Security Group — allows TCP 5000 (HTTP) and 5002 (gRPC) inbound
  2. IAM Role — AmazonSSMManagedInstanceCore for SSM access
  3. IAM Instance Profile — attaches the role to the EC2 instance
  4. EC2 Instance — runs the Horde coordinator using a user-provided AMI

State is written after each resource so a partial failure is recoverable:
re-running create will detect the already-provisioned module and exit cleanly.

A MongoDB password is generated and written to .fabrica/horde-credentials.yaml.
With Docker compose AMIs, this password is validated but not applied by cloud-init
(the compose stack manages MongoDB credentials independently).

Operator access to the instance is via AWS Systems Manager Session Manager
(not public SSH). The instance has no inbound SSH from the internet.

With --dry-run, shows the provisioning plan and a monthly cost estimate without
making any AWS calls.`,
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
				instanceType: instanceType,
				volumeSize:   volumeSize,
				out:          out,
				costs:        fabricacost.Global,
				confirm:      prompt.ConfirmExact,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
			}
			return c.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: m7i.2xlarge)")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "EBS data volume size in GiB (default: 100)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
	}

	hordeCfg := c.runtime.Config.Horde
	if c.instanceType != "" {
		hordeCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		hordeCfg.VolumeSize = c.volumeSize
	}

	var cidrResolver cloud.VPCCIDRResolver
	if cr, ok := c.runtime.Provider.(cloud.VPCCIDRResolver); ok {
		cidrResolver = cr
	}

	plan, err := horde.NewCreatePlan(ctx, hordeCfg, account, region, provision.VPCResolver(c.runtime.Provider), cidrResolver)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	return provision.RunCreate(ctx, provision.CreateSpec[*horde.CreatePlan]{
		ModuleName:      moduleName,
		Account:         account,
		Plan:            plan,
		DryRun:          c.dryRun,
		AssumeYes:       c.assumeYes,
		Out:             c.out,
		ExistingMessage: "Horde is already provisioned. Run 'fabrica horde status' to check health.\nUse 'fabrica horde destroy' to remove it first.\n",
		Confirm:         c.confirm,
		ReadState:       c.readState,
		PrintDryRun:     c.printDryRun,
		PrintApplyPlan:  c.printApplyPlan,
		Apply:           c.applyCreate,
	})
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *horde.CreatePlan) error {
	genPass := c.genPassword
	if genPass == nil {
		genPass = credentials.GeneratePassword
	}
	mongoPass, err := genPass(passwordLen)
	if err != nil {
		return fmt.Errorf("generating MongoDB password: %w", err)
	}

	if err := credentials.WriteCredentials(credFile, credentials.FormatHorde(mongoPass)); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nMongoDB credentials written to %s\n", credFile)

	var resources []fabricastate.ModuleResource

	// Create Security Group
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Security group",
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) {
			return horde.SGDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

	// IAM Role
	fmt.Fprintf(c.out, "Creating IAM role %s...\n", plan.RoleName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "IAM role",
		TypeName:          cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) { return horde.RoleDesiredState(plan) },
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating IAM role: %w", err)
	}

	// Instance Profile
	fmt.Fprintf(c.out, "Creating instance profile %s...\n", plan.InstanceProfileName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Instance profile",
		TypeName:          cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) { return horde.InstanceProfileDesiredState(plan) },
		ResourceIdentifier: func(created *cloud.Resource) string {
			name := plan.InstanceProfileName
			if created.Identifier != "" && !strings.HasPrefix(created.Identifier, "arn:") {
				name = created.Identifier
			}
			return name
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating instance profile: %w", err)
	}
	profileName := resources[len(resources)-1].Identifier

	// Create EC2 Instance
	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)
	genUserData := c.genUserData
	if genUserData == nil {
		genUserData = horde.Generate
	}
	userData, err := genUserData(horde.UserDataConfig{
		MongoPassword: mongoPass,
		Port:          plan.Port,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return horde.InstanceDesiredState(plan, sgID, userData, profileName)
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

func (c command) printDryRun(plan *horde.CreatePlan) {
	provision.DryRun(c.out, provision.DryRunSpec{
		Title: "Horde build coordinator",
		Info: provision.PlanInfo{
			Account:      plan.Account,
			Region:       plan.Region,
			InstanceType: plan.InstanceType,
			VolumeSize:   plan.VolumeSize,
			AllowedCIDR:  plan.AllowedCIDR,
			VPCID:        plan.VPCID,
			DefaultVPC:   plan.DefaultVPC,
		},
		ExtraFields: []provision.PlanField{
			{Key: "AMI ID", Value: plan.AmiID},
			{Key: "HTTP port", Value: fmt.Sprintf("%d", plan.Port)},
			{Key: "gRPC port", Value: fmt.Sprintf("%d", plan.GRPCPort)},
			{Key: "Allowed CIDR", Value: plan.AllowedCIDR},
		},
		Resources: []string{
			"Security Group:   " + plan.SGName,
			"IAM Role:         " + plan.RoleName,
			"Instance Profile: " + plan.InstanceProfileName,
			"EC2 Instance:     " + plan.InstanceName,
		},
		CostResources: plan.CostResources,
		Costs:         c.costs,
		RawBetween: func(w io.Writer) {
			if plan.AllowedCIDR == "0.0.0.0/0" {
				fmt.Fprintln(w, "  Warning: allowedCidr is 0.0.0.0/0 — ports 5000 and 5002 are open")
				fmt.Fprintln(w, "           to the internet. Restrict this in fabrica.yaml before connecting")
				fmt.Fprintln(w, "           agents or running production workloads.")
			}
		},
	})
}

func (c command) printApplyPlan(plan *horde.CreatePlan) {
	provision.ApplyPlan(c.out, "Horde build coordinator", provision.PlanInfo{
		Account:      plan.Account,
		Region:       plan.Region,
		InstanceType: plan.InstanceType,
		VolumeSize:   plan.VolumeSize,
	}, []provision.PlanField{
		{Key: "AMI ID", Value: plan.AmiID},
	}, []string{
		"Security Group:   " + plan.SGName,
		"IAM Role:         " + plan.RoleName,
		"Instance Profile: " + plan.InstanceProfileName,
		"EC2 Instance:     " + plan.InstanceName,
	})
}

func (c command) printPostCreate(plan *horde.CreatePlan, instanceID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Horde coordinator",
		InstanceID:   instanceID,
		StatusDetail: "provisioning (Horde starting up, ~3 min)",
		Details: []provision.PlanField{
			{Key: "Horde HTTP", Value: fmt.Sprintf("http://<private-ip>:%d", plan.Port)},
			{Key: "Horde gRPC", Value: fmt.Sprintf("<private-ip>:%d", plan.GRPCPort)},
			{Key: "Credentials", Value: credFile},
		},
		NextSteps: []string{
			"fabrica horde status -w       Wait for coordinator to become ready",
			fmt.Sprintf("Open http://<private-ip>:%d   Complete admin account setup in the web UI", plan.Port),
			"fabrica horde submit <file>   Submit a BuildGraph job",
		},
		RawAfter: func(w io.Writer) {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  Note: Horde is accessible via the instance's private IP. Ensure your")
			fmt.Fprintln(w, "        machine can reach it (VPN, VPC peering, or same-VPC access).")
			fmt.Fprintln(w, "        To allow broader access, update horde.allowedCidr in fabrica.yaml.")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  Operator shell access is via AWS Systems Manager Session Manager:")
			fmt.Fprintf(w, "    aws ssm start-session --target %s\n", instanceID)
			if plan.AllowedCIDR == "0.0.0.0/0" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, "  Warning: horde.allowedCidr is 0.0.0.0/0 — ports 5000 and 5002 are open")
				fmt.Fprintln(w, "           to the internet. Restrict this in fabrica.yaml before connecting")
				fmt.Fprintln(w, "           agents or running production workloads.")
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "If the coordinator doesn't become ready within 10 minutes, check:")
			fmt.Fprintln(w, "  /var/log/fabrica-horde-init.log  on the instance")
		},
	})
}
