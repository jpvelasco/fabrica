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
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
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
	resolveAMI     func(ctx context.Context, region string) (string, error)
	inspectImage   func(ctx context.Context, imageID string) (cloud.ImageInfo, error)
	genPassword    func(int) (string, error)
	writeCreds     func(string, string) error
	// putSecureParameter and deleteParameter are the SecureString seams (#700).
	putSecureParameter func(context.Context, cloud.SecureParameter) error
	deleteParameter    func(context.Context, string) error
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

State is written after each resource, so an interrupted create leaves a
recoverable record. Re-running create on a complete module exits cleanly; on
a partial one it fails and names the missing resources plus the cleanup
command to run first.

A random admin password is generated and written to .fabrica/perforce-credentials.yaml
(the operator record) and stored in SSM as a per-create SecureString. The instance
reads it at boot; it never appears in EC2 user data. Rotate it after first login.

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
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(cmd.Context(), rt) }
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
				if resolver, ok := rt.Provider.(cloud.AMIResolver); ok {
					c.resolveAMI = resolver.ResolveUbuntuAMI
				}
				if insp, ok := rt.Provider.(cloud.ImageInspector); ok {
					c.inspectImage = insp.DescribeImage
				}
				c.putSecureParameter = provision.ParameterPut(rt.Provider)
				c.deleteParameter = provision.ParameterDelete(rt.Provider)
			}
			return c.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: m5.xlarge)")
	cmd.Flags().StringVar(&version, "version", "", "Helix Core version: \"latest\", \"YYYY.N\", or \"YYYY.N/BUILD\"; create fails if the Perforce repo lacks it (default: "+perforce.DefaultHelixVersion+")")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "EBS data volume size in GiB (default: 500)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
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

	resolvedIAM, err := config.ResolveIAM(c.runtime.Config.Cloud.AWS.IAM, pfCfg.IAM)
	if err != nil {
		return err
	}
	pfCfg.IAM = resolvedIAM

	plan, err := perforce.NewCreatePlan(ctx, pfCfg, account, region, effectiveVersion, provision.VPCResolver(c.runtime.Provider))
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	// Mint the per-create admin-password name before any plan is printed or
	// applied, so the role (scoped to this name) and cloud-init (which reads it
	// at boot) agree with the SecureString apply writes (#700).
	if plan.AdminPasswordParameter, err = perforce.AdminPasswordScope.NewName(); err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	return provision.RunCreate(ctx, provision.CreateSpec[*perforce.CreatePlan]{
		ModuleName:        moduleName,
		Account:           account,
		Plan:              plan,
		DryRun:            c.dryRun,
		AssumeYes:         c.assumeYes,
		Out:               c.out,
		ExistingMessage:   "Perforce is already provisioned. Run 'fabrica perforce status' to check health.\nUse 'fabrica perforce destroy' to remove it first.\n",
		ReadyStatuses:     []string{"ready"},
		RequiredResources: plan.RequiredResources(),
		CleanupCommand:    "fabrica perforce destroy",
		Confirm:           c.confirm,
		ReadState:         c.readState,
		PrintDryRun:       c.printDryRun,
		PrintApplyPlan:    c.printApplyPlan,
		Apply:             c.applyCreate,
		Runtime:           c.runtime,
		Operation:         "perforce create",
	})
}

// launch is the instance launch input gathered before the instance step.
type launch struct {
	sgID, profileName, imageID, userData string
}

// applyCreate executes the provisioning plan. The admin password is stored as
// a SecureString first. Until the instance exists, a failure deletes that
// password; once it exists, terminate owns the cleanup (#700). State is
// persisted after each successful creation so partial failures leave a
// recoverable record.
func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *perforce.CreatePlan) error {
	c.writeState = provision.BindStateWriter(ctx, c.runtime, c.writeState)
	if c.putSecureParameter == nil {
		return fmt.Errorf("cannot store the Helix Core admin password: the provider has no SSM parameter store, so the instance could not read it at boot; refusing to create anything")
	}
	adminPass, err := c.generateAndWriteCredentials()
	if err != nil {
		return err
	}
	if err := c.storeAdminPassword(ctx, plan, adminPass); err != nil {
		return err
	}

	resources, sgID, profileName, err := c.createSecurityGroupAndIAM(ctx, st, plan)
	if err != nil {
		return provision.AbandonSecureParameter(ctx, c.deleteParameter, plan.AdminPasswordParameter, err)
	}
	imageID, userData, err := c.resolveLaunch(ctx, plan)
	if err != nil {
		return provision.AbandonSecureParameter(ctx, c.deleteParameter, plan.AdminPasswordParameter, err)
	}
	return c.createInstance(ctx, st, plan, resources, launch{sgID: sgID, profileName: profileName, imageID: imageID, userData: userData})
}

// storeAdminPassword writes the admin password to its per-create SecureString.
// The name is never reused, so a write never clobbers another create's secret.
func (c command) storeAdminPassword(ctx context.Context, plan *perforce.CreatePlan, adminPass string) error {
	name := plan.AdminPasswordParameter
	if err := c.putSecureParameter(ctx, cloud.SecureParameter{
		Name:  name,
		Value: adminPass,
		Tags:  c.runtime.Config.Cloud.AWS.Tags,
	}); err != nil {
		return fmt.Errorf("storing the Helix Core admin password in SSM parameter %s: %w", name, err)
	}
	fmt.Fprintf(c.out, "Stored Helix Core admin password in SSM parameter %s\n", name)
	return nil
}

// createSecurityGroupAndIAM creates the security group, IAM role, and instance
// profile, and returns the resource list with the SG and profile identifiers.
func (c command) createSecurityGroupAndIAM(ctx context.Context, st *fabricastate.State, plan *perforce.CreatePlan) ([]fabricastate.ModuleResource, string, string, error) {
	var resources []fabricastate.ModuleResource

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)
	resources, err := provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Security group",
		TypeName:          cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) { return perforce.SGDesiredState(plan) },
	}, moduleName, plan.HelixVersion, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return nil, "", "", fmt.Errorf("creating security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

	fmt.Fprintf(c.out, "Creating IAM role %s...\n", plan.RoleName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "IAM role",
		TypeName:          cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) { return perforce.RoleDesiredState(plan) },
	}, moduleName, plan.HelixVersion, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return nil, "", "", fmt.Errorf("creating IAM role: %w", err)
	}

	fmt.Fprintf(c.out, "Creating instance profile %s...\n", plan.InstanceProfileName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Instance profile",
		TypeName:          cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) { return perforce.InstanceProfileDesiredState(plan) },
		ResourceIdentifier: func(created *cloud.Resource) string {
			name := plan.InstanceProfileName
			if created.Identifier != "" && !strings.HasPrefix(created.Identifier, "arn:") {
				name = created.Identifier
			}
			return name
		},
	}, moduleName, plan.HelixVersion, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return nil, "", "", fmt.Errorf("creating instance profile: %w", err)
	}
	return resources, sgID, resources[len(resources)-1].Identifier, nil
}

// resolveLaunch resolves the AMI, reads its root device, and renders the
// cloud-init script. The script names the admin-password parameter; it does
// not carry the password.
func (c command) resolveLaunch(ctx context.Context, plan *perforce.CreatePlan) (string, string, error) {
	userData, err := perforce.Generate(perforce.UserDataConfig{
		Version:                plan.HelixVersion,
		ServerID:               plan.InstanceName,
		Region:                 plan.Region,
		AdminPasswordParameter: plan.AdminPasswordParameter,
	})
	if err != nil {
		return "", "", fmt.Errorf("generating user data: %w", err)
	}

	imageID, err := c.resolveImageID(ctx, plan.Region)
	if err != nil {
		return "", "", err
	}
	if imageID == "" {
		return "", "", fmt.Errorf("no Ubuntu AMI could be resolved in %s, so the Perforce instance cannot be launched", plan.Region)
	}
	rootDevice, err := c.rootDeviceForAMI(ctx, imageID)
	if err != nil {
		return "", "", err
	}
	plan.RootDeviceName = rootDevice
	return imageID, userData, nil
}

// createInstance launches the EC2 instance. A failure before the instance
// exists abandons the stored admin password; a failure after it (volume
// tagging or the state write) keeps the password for cloud-init and terminate.
func (c command) createInstance(ctx context.Context, st *fabricastate.State, plan *perforce.CreatePlan, resources []fabricastate.ModuleResource, l launch) error {
	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)

	// Set only when Create itself returns nil. ExecuteStep also fails when the
	// following state write fails, and in that case the instance already exists.
	instanceCreated := false
	createFn := func(ctx context.Context, r *cloud.Resource) error {
		if err := c.createResource(ctx, r); err != nil {
			return err
		}
		instanceCreated = true
		return nil
	}

	resources, err := provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return perforce.InstanceDesiredState(plan, l.sgID, l.userData, l.profileName, l.imageID)
		},
		// The admin-password name is recorded on the instance (not as a state
		// resource, which a drift Get would read back as the secret). Terminate
		// and drift rebuild read it from here.
		Properties: map[string]string{
			"instanceType":           plan.InstanceType,
			"volumeSize":             strconv.Itoa(plan.VolumeSize),
			"imageId":                l.imageID,
			"rootDevice":             plan.RootDeviceName,
			"adminPasswordParameter": plan.AdminPasswordParameter,
		},
		PostCreate: provision.TagVolumesPostCreate(c.runtime.Provider, "perforce", plan.InstanceName),
	}, moduleName, plan.HelixVersion, "provisioning", resources, st, c.out, createFn, c.writeState)
	if err != nil {
		wrapped := fmt.Errorf("creating EC2 instance: %w", err)
		if instanceCreated {
			return wrapped
		}
		return provision.AbandonSecureParameter(ctx, c.deleteParameter, plan.AdminPasswordParameter, wrapped)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

func (c command) generateAndWriteCredentials() (string, error) {
	genPass := c.genPassword
	if genPass == nil {
		genPass = credentials.GeneratePassword
	}
	adminPass, err := genPass(passwordLen)
	if err != nil {
		return "", fmt.Errorf("generating admin password: %w", err)
	}

	writeCreds := c.writeCreds
	if writeCreds == nil {
		writeCreds = credentials.WriteCredentials
	}
	if err := writeCreds(credFile, credentials.FormatPerforce(adminPass)); err != nil {
		return "", fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nAdmin credentials written to %s\n", credFile)
	fmt.Fprintln(c.out, "Warning: Rotate the admin password after first login.")
	return adminPass, nil
}

func (c command) resolveImageID(ctx context.Context, region string) (string, error) {
	if c.resolveAMI == nil {
		return "", nil
	}
	imageID, err := c.resolveAMI(ctx, region)
	if err != nil {
		return "", fmt.Errorf("resolving Ubuntu AMI: %w", err)
	}
	fmt.Fprintf(c.out, "  Resolved AMI: %s\n", imageID)
	return imageID, nil
}

// rootDeviceForAMI reads the AMI's root device so the encrypted root mapping
// lands on the real root (#513). A provider that cannot inspect AMIs refuses
// the launch rather than guessing a device: a mapping on the wrong name would
// boot the root unencrypted from the snapshot.
func (c command) rootDeviceForAMI(ctx context.Context, imageID string) (string, error) {
	if c.inspectImage == nil {
		return "", fmt.Errorf("cannot read the root device of the perforce AMI %s (the provider cannot inspect AMIs), so the root volume cannot be encrypted; refusing to launch", imageID)
	}
	info, err := c.inspectImage(ctx, imageID)
	if err != nil {
		return "", fmt.Errorf("inspecting perforce AMI %s: %w", imageID, err)
	}
	return ec2state.RootDeviceFor("the perforce AMI", imageID, info.RootDeviceName, info.RootDeviceType)
}

func (c command) printDryRun(plan *perforce.CreatePlan) {
	versionLabel := plan.HelixVersion
	if plan.HelixVersion != "latest" {
		versionLabel += " (pinned)"
	}
	provision.DryRun(c.out, provision.DryRunSpec{
		Title: "Perforce Helix Core",
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
			{Key: "Helix Core", Value: versionLabel},
		},
		Resources: append([]string{
			"Security Group:   " + plan.SGName,
			"IAM Role:         " + plan.RoleName,
			"Instance Profile: " + plan.InstanceProfileName,
			"EC2 Instance:     " + plan.InstanceName,
		}, provision.IAMResourceLines(plan.IAM)...),
		CostResources: plan.CostResources,
		Costs:         c.costs,
		CidrWarning:   "P4 port (1666) open to the entire internet. Set perforce.allowedCidr in fabrica.yaml.",
	})
}

func (c command) printApplyPlan(plan *perforce.CreatePlan) {
	provision.ApplyPlan(c.out, "Perforce Helix Core", provision.PlanInfo{
		Account:      plan.Account,
		Region:       plan.Region,
		InstanceType: plan.InstanceType,
		VolumeSize:   plan.VolumeSize,
	}, []provision.PlanField{
		{Key: "Helix Core", Value: plan.HelixVersion},
	}, append([]string{
		"Security Group:   " + plan.SGName,
		"IAM Role:         " + plan.RoleName,
		"Instance Profile: " + plan.InstanceProfileName,
		"EC2 Instance:     " + plan.InstanceName,
	}, provision.IAMResourceLines(plan.IAM)...))
}

func (c command) printPostCreate(_ *perforce.CreatePlan, instanceID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Perforce Helix Core",
		InstanceID:   instanceID,
		StatusDetail: "provisioning (Helix Core setup in progress, ~3 min)",
		Details: []provision.PlanField{
			{Key: "Admin credentials", Value: credFile},
		},
		NextSteps: []string{
			"fabrica perforce status      Check readiness",
		},
		RawAfter: func(w io.Writer) {
			fmt.Fprintln(w, "  Warning: Rotate the admin password after first login.")
		},
	})
}
