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
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth   = 58
	moduleName  = "perforce"
	credFile    = ".fabrica/perforce-credentials.yaml"
	passwordLen = 24
)

type command struct {
	runtime      globals.Runtime
	dryRun       bool
	assumeYes    bool
	instanceType string
	version      string
	volumeSize   int
	out          io.Writer
	costs        *fabricacost.Registry
	confirm      func(string, string) bool

	// seams for testing
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
}

// New returns the "perforce create" subcommand. It accepts RuntimeSource and
// OptionsSource closures so that global flags (--dry-run, --yes, --json) are
// resolved at execution time rather than at construction time.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType, version string
	var volumeSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a Perforce Helix Core server",
		Long: `Provision a Perforce Helix Core server on AWS.

Creates two resources in order:
  1. EC2 Security Group — allows TCP 1666 inbound (Perforce p4d port)
  2. EC2 Instance — runs Helix Core with a dedicated gp3 EBS data volume

State is written after each resource so a partial failure is recoverable:
re-running create will detect the already-provisioned module and exit cleanly.

A random admin password is generated and written to .fabrica/perforce-credentials.yaml.
Rotate it after first login.

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
				version:      version,
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
	cmd.Flags().StringVar(&version, "version", "", "Helix Core version: \"latest\", \"2024.2\", or \"2024.2/BUILD\" (default: 2024.2)")
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

	// Resolve version: flag > config > default
	pfCfg := c.runtime.Config.Perforce
	effectiveVersion := perforce.ResolveVersion(c.version, pfCfg.Version)
	if c.instanceType != "" {
		pfCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		pfCfg.VolumeSize = c.volumeSize
	}

	plan, err := perforce.NewCreatePlan(ctx, pfCfg, account, region, effectiveVersion, nil)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	// Check for existing module state
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if m := st.GetModule(moduleName); m != nil {
		fmt.Fprintf(c.out, "Perforce is already provisioned. Run 'fabrica perforce status' to check health.\n")
		fmt.Fprintf(c.out, "Use 'fabrica perforce destroy' to remove it first.\n")
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

// applyCreate executes the provisioning plan: generates credentials, creates the
// security group, then creates the EC2 instance. State is persisted after each
// successful creation so partial failures leave a recoverable record.
func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *perforce.CreatePlan) error {
	adminPass, err := credentials.GeneratePassword(passwordLen)
	if err != nil {
		return fmt.Errorf("generating admin password: %w", err)
	}

	if err := credentials.WriteCredentials(credFile, credentials.FormatPerforce(adminPass)); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nAdmin credentials written to %s\n", credFile)
	fmt.Fprintln(c.out, "Warning: Rotate the admin password after first login.")
	fmt.Fprintln(c.out, "         Restrict ec2:DescribeInstanceAttribute to limit exposure.")

	// Create Security Group
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	sgDesired, err := perforce.SGDesiredState(plan)
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

	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
	}
	st.UpsertModule(moduleName, plan.HelixVersion, "provisioning", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after SG creation: %w", err)
	}

	// IAM role for SSM (backup/restore) + optional S3 export
	fmt.Fprintf(c.out, "Creating IAM role %s...\n", plan.RoleName)
	roleDesired, err := perforce.RoleDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building IAM role desired state: %w", err)
	}
	role := &cloud.Resource{
		TypeName:     "AWS::IAM::Role",
		DesiredState: roleDesired,
	}
	if err := c.createResource(ctx, role); err != nil {
		return fmt.Errorf("creating IAM role: %w", err)
	}
	fmt.Fprintf(c.out, "  IAM role created: %s\n", role.Identifier)
	resources = append(resources, fabricastate.ModuleResource{
		TypeName: "AWS::IAM::Role", Identifier: role.Identifier,
	})
	st.UpsertModule(moduleName, plan.HelixVersion, "provisioning", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after IAM role creation: %w", err)
	}

	fmt.Fprintf(c.out, "Creating instance profile %s...\n", plan.InstanceProfileName)
	profileDesired, err := perforce.InstanceProfileDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building instance profile desired state: %w", err)
	}
	profile := &cloud.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: profileDesired,
	}
	if err := c.createResource(ctx, profile); err != nil {
		return fmt.Errorf("creating instance profile: %w", err)
	}
	fmt.Fprintf(c.out, "  Instance profile created: %s\n", profile.Identifier)
	// Prefer the plan name for IamInstanceProfile.Name (identifier may be ARN).
	profileName := plan.InstanceProfileName
	if profile.Identifier != "" && !strings.HasPrefix(profile.Identifier, "arn:") {
		profileName = profile.Identifier
	}
	resources = append(resources, fabricastate.ModuleResource{
		TypeName: "AWS::IAM::InstanceProfile", Identifier: profile.Identifier,
	})
	st.UpsertModule(moduleName, plan.HelixVersion, "provisioning", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after instance profile creation: %w", err)
	}

	// Create EC2 Instance
	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)

	userData, err := perforce.Generate(perforce.UserDataConfig{
		Version:   plan.HelixVersion,
		ServerID:  plan.InstanceName,
		AdminPass: adminPass,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	instanceDesired, err := perforce.InstanceDesiredState(plan, sg.Identifier, userData, profileName)
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

	resources = append(resources, fabricastate.ModuleResource{
		TypeName: "AWS::EC2::Instance", Identifier: instance.Identifier, Properties: map[string]string{
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
	})
	st.UpsertModule(moduleName, plan.HelixVersion, "provisioning", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after instance creation: %w", err)
	}

	c.printPostCreate(plan, instance.Identifier)
	return nil
}

func (c command) printDryRun(plan *perforce.CreatePlan) {
	fmt.Fprintln(c.out, "Perforce Helix Core (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))

	versionLabel := plan.HelixVersion
	if plan.HelixVersion != "latest" {
		versionLabel += " (pinned)"
	}

	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Helix Core:       %s\n", versionLabel)
	fmt.Fprintf(c.out, "  Data volume:      %d GiB gp3\n", plan.VolumeSize)
	if plan.DefaultVPC {
		fmt.Fprintf(c.out, "  VPC:              default (%s)\n", plan.VPCID)
		fmt.Fprintln(c.out, "  Note:             Default VPC used. Configure a dedicated VPC for production.")
	} else if plan.VPCID != "" {
		fmt.Fprintf(c.out, "  VPC:              %s\n", plan.VPCID)
	}
	fmt.Fprintln(c.out)

	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  IAM Role:         %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  Instance Profile: %s\n", plan.InstanceProfileName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
	fmt.Fprintln(c.out)

	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printApplyPlan(plan *perforce.CreatePlan) {
	fmt.Fprintln(c.out, "Perforce Helix Core")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Helix Core:       %s\n", plan.HelixVersion)
	fmt.Fprintf(c.out, "  Data volume:      %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  IAM Role:         %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  Instance Profile: %s\n", plan.InstanceProfileName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
}

func (c command) printPostCreate(_ *perforce.CreatePlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Perforce Helix Core provisioned.")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Instance ID:   %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:        provisioning (Helix Core setup in progress, ~3 min)\n")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Admin credentials: %s\n", credFile)
	fmt.Fprintln(c.out, "  Warning: Rotate the admin password after first login.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica perforce status      Check readiness")
}
