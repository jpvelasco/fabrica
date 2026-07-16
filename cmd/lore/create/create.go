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
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "lore"
	credFile   = ".fabrica/lore-credentials.yaml" // #nosec G101 -- file path, not a credential
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
}

// New returns the "lore create" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType string
	var volumeSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a Lore version control server",
		Long: `Provision an Epic Lore (loreserver) version control server on AWS.

Creates two resources in order:
  1. EC2 Security Group — TCP 41337 (gRPC), UDP 41337 (QUIC), TCP 41339 (HTTP)
  2. EC2 Instance — runs loreserver using a user-provided AMI (local/EBS store)

State is written after each resource so a partial failure is recoverable:
re-running create will detect the already-provisioned module and exit cleanly.

Connection notes are written to .fabrica/lore-credentials.yaml (self-signed TLS;
no JWT in V1).

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

	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: m5.xlarge)")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "EBS data volume size in GiB (default: 500)")
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

	loreCfg := c.runtime.Config.Lore
	if c.instanceType != "" {
		loreCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		loreCfg.VolumeSize = c.volumeSize
	}

	plan, err := lore.NewCreatePlan(ctx, loreCfg, account, region, nil)
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
		fmt.Fprintln(c.out, "Lore is already provisioned. Run 'fabrica lore status' to check health.")
		fmt.Fprintln(c.out, "Use 'fabrica lore destroy' to remove it first.")
		return nil
	}

	c.printApplyPlan(plan)

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := provision.ConfirmPhrase(moduleName, account)
		provision.PrintConfirmInstructions(c.out, phrase)
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

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *lore.CreatePlan) error {
	if err := credentials.WriteCredentials(credFile, credentials.FormatLore(plan.GRPCPort, plan.HTTPPort)); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nConnection notes written to %s\n", credFile)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	sgDesired, err := lore.SGDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building SG desired state: %w", err)
	}
	sg := &cloud.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: sgDesired,
	}
	if err := c.createResource(ctx, sg); err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	fmt.Fprintf(c.out, "  Security group created: %s\n", sg.Identifier)

	props := map[string]string{
		"instanceType": plan.InstanceType,
		"volumeSize":   strconv.Itoa(plan.VolumeSize),
	}
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after SG creation: %w", err)
	}

	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)

	userData, err := lore.Generate(lore.UserDataConfig{
		StorePath: lore.DefaultStorePath,
		ConfigDir: lore.DefaultConfigDir,
		GRPCPort:  plan.GRPCPort,
		HTTPPort:  plan.HTTPPort,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	instanceDesired, err := lore.InstanceDesiredState(plan, sg.Identifier, userData)
	if err != nil {
		return fmt.Errorf("building instance desired state: %w", err)
	}
	instance := &cloud.Resource{
		TypeName:     "AWS::EC2::Instance",
		DesiredState: instanceDesired,
	}
	if err := c.createResource(ctx, instance); err != nil {
		return fmt.Errorf("creating EC2 instance: %w", err)
	}
	fmt.Fprintf(c.out, "  Instance created: %s\n", instance.Identifier)

	st.UpsertModule(moduleName, plan.AmiID, "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
		{TypeName: "AWS::EC2::Instance", Identifier: instance.Identifier, Properties: props},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after instance creation: %w", err)
	}

	c.printPostCreate(plan, instance.Identifier)
	return nil
}

func (c command) printDryRun(plan *lore.CreatePlan) {
	fmt.Fprintln(c.out, "Lore loreserver (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Data volume:      %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintf(c.out, "  gRPC/QUIC port:   %d (tcp+udp)\n", plan.GRPCPort)
	fmt.Fprintf(c.out, "  HTTP port:        %d\n", plan.HTTPPort)
	fmt.Fprintf(c.out, "  Allowed CIDR:     %s\n", plan.AllowedCIDR)
	if plan.DefaultVPC {
		fmt.Fprintf(c.out, "  VPC:              default (%s)\n", plan.VPCID)
		fmt.Fprintln(c.out, "  Note:             Default VPC used. Configure a dedicated VPC for production.")
	} else if plan.VPCID != "" {
		fmt.Fprintf(c.out, "  VPC:              %s\n", plan.VPCID)
	}
	if plan.AllowedCIDR == "0.0.0.0/0" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  WARNING: allowedCidr is 0.0.0.0/0 — Lore ports are open")
		fmt.Fprintln(c.out, "           to the internet. Restrict this in fabrica.yaml before production use.")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
	fmt.Fprintln(c.out)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printApplyPlan(plan *lore.CreatePlan) {
	fmt.Fprintln(c.out, "Lore loreserver")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Data volume:      %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
}

func (c command) printPostCreate(plan *lore.CreatePlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Lore server provisioned.")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Instance ID:    %s\n", instanceID)
	fmt.Fprintln(c.out, "  Status:         provisioning (loreserver starting up, ~3 min)")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  gRPC/QUIC:      <private-ip>:%d (tcp+udp)\n", plan.GRPCPort)
	fmt.Fprintf(c.out, "  HTTP health:    http://<private-ip>:%d/health_check\n", plan.HTTPPort)
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Connection notes: %s\n", credFile)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  Note: Lore is accessible via the instance's private IP. Ensure your")
	fmt.Fprintln(c.out, "        machine can reach it (VPN, VPC peering, or same-VPC access).")
	fmt.Fprintln(c.out, "        TLS is self-signed in V1; clients must trust the cert.")
	if plan.AllowedCIDR == "0.0.0.0/0" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  WARNING: lore.allowedCidr is 0.0.0.0/0 — ports are open to the internet.")
		fmt.Fprintln(c.out, "           Restrict this in fabrica.yaml before production use.")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  1. fabrica lore status -w       Wait for server to become ready")
	fmt.Fprintln(c.out, "  2. Point Lore clients at the private IP (see connection notes)")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "If the server doesn't become ready within 10 minutes, check:")
	fmt.Fprintln(c.out, "  /var/log/fabrica-lore-init.log  on the instance")
}
