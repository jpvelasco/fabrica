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
	inspectImage   func(ctx context.Context, imageID string) (cloud.ImageInfo, error)
	genPassword    func(int) (string, error)
	genUserData    func(horde.UserDataConfig) (string, error)
	newNonce       func() (string, error)

	putSecureParameter func(ctx context.Context, p cloud.SecureParameter) error
	deleteParameter    func(ctx context.Context, name string) error
}

// New returns the "horde create" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType string
	var volumeSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision an Unreal Horde build coordinator",
		Long: `Provision an Unreal Horde build coordinator on AWS.

Creates these resources in order:
  1. SSM SecureString — /fabrica/horde/<create-nonce>/service-token, a
     placeholder cloud-init overwrites with the Horde service-account token
  2. EC2 Security Group — allows TCP 5000 (HTTP) and 5002 (gRPC) inbound
  3. IAM Role — AmazonSSMManagedInstanceCore for SSM access, plus
     ssm:PutParameter on exactly the service-token parameter
  4. IAM Instance Profile — attaches the role to the EC2 instance
  5. EC2 Instance — runs the Horde coordinator using a user-provided AMI

State is written after each resource, so an interrupted create leaves a
recoverable record. Re-running create on a complete module exits cleanly; on
a partial one it fails and names the missing resources plus the cleanup
command to run first.

Horde runs with its built-in accounts. Cloud-init generates the MongoDB and
Admin passwords on the instance, creates the Admin account while Horde is
bound to loopback, and creates the fabrica-ci service account whose token
'fabrica horde submit' and the ci module read from the service-token
parameter. Read the Admin password over SSM from /etc/horde/admin-password.

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
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(cmd.Context(), rt) }
			c.writeState = provision.StateWriter(cmd.Context(), rt)
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
				if insp, ok := rt.Provider.(cloud.ImageInspector); ok {
					c.inspectImage = insp.DescribeImage
				}
				if store, ok := rt.Provider.(cloud.ParameterStore); ok {
					c.putSecureParameter = store.PutSecureParameter
					c.deleteParameter = store.DeleteParameter
				}
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

	resolvedIAM, err := config.ResolveIAM(c.runtime.Config.Cloud.AWS.IAM, hordeCfg.IAM)
	if err != nil {
		return err
	}
	hordeCfg.IAM = resolvedIAM

	plan, err := horde.NewCreatePlan(ctx, hordeCfg, account, region, provision.VPCResolver(c.runtime.Provider), cidrResolver)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	rootDevice, err := c.inspectAMI(ctx, plan.AmiID)
	if err != nil {
		return err
	}
	if rootDevice == "" && !c.dryRun {
		// Without the AMI's own root device there is no way to put the
		// encrypted mapping on the real root; a guessed device would boot the
		// root unencrypted from the snapshot (#507, #513). Fail before any write.
		return fmt.Errorf("cannot read the root device of horde.amiId %s (the provider cannot inspect AMIs), so the root volume cannot be encrypted; refusing to launch", plan.AmiID)
	}
	plan.RootDeviceName = rootDevice

	// Mint the per-create service-token parameter name before the plan is
	// printed or applied, so the previews, the role scope, cloud-init, and the
	// state record all name the same SecureString.
	newNonce := c.newNonce
	if newNonce == nil {
		newNonce = horde.NewServiceTokenNonce
	}
	nonce, err := newNonce()
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}
	if plan.ServiceTokenParameter, err = horde.ServiceTokenParameterFor(nonce); err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	return provision.RunCreate(ctx, provision.CreateSpec[*horde.CreatePlan]{
		ModuleName:        moduleName,
		Account:           account,
		Plan:              plan,
		DryRun:            c.dryRun,
		AssumeYes:         c.assumeYes,
		Out:               c.out,
		ExistingMessage:   "Horde is already provisioned. Run 'fabrica horde status' to check health.\nUse 'fabrica horde destroy' to remove it first.\n",
		ReadyStatuses:     []string{"ready"},
		RequiredResources: plan.RequiredResources(),
		CleanupCommand:    "fabrica horde destroy",
		Confirm:           c.confirm,
		ReadState:         c.readState,
		PrintDryRun:       c.printDryRun,
		PrintApplyPlan:    c.printApplyPlan,
		Apply:             c.applyCreate,
		Runtime:           c.runtime,
		Operation:         "horde create",
	})
}

// inspectAMI reads the AMI's root device so the encrypted root mapping lands
// on the real root (#513). A provider that cannot inspect AMIs returns an
// empty device; run refuses to apply in that case.
func (c command) inspectAMI(ctx context.Context, amiID string) (string, error) {
	if c.inspectImage == nil {
		return "", nil
	}
	info, err := c.inspectImage(ctx, amiID)
	if err != nil {
		return "", fmt.Errorf("inspecting horde.amiId %s: %w", amiID, err)
	}
	return ec2state.RootDeviceFor("horde.amiId", amiID, info.RootDeviceName, info.RootDeviceType)
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *horde.CreatePlan) error {
	if c.putSecureParameter == nil || c.deleteParameter == nil {
		return fmt.Errorf("storing the Horde service token requires a provider with SSM parameter support. Run 'fabrica setup' with the AWS provider.")
	}
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

	// Pre-create the service-token SecureString with a placeholder and the
	// operator's tags. The instance role may only overwrite this one name;
	// cloud-init replaces the placeholder with the real token.
	tokenParam := plan.ServiceTokenParameter
	if err := c.putSecureParameter(ctx, cloud.SecureParameter{
		Name:  tokenParam,
		Value: horde.ServiceTokenPlaceholder,
		Tags:  c.runtime.Config.Cloud.AWS.Tags,
	}); err != nil {
		return fmt.Errorf("creating the service-token SSM parameter %s: %w", tokenParam, err)
	}
	fmt.Fprintf(c.out, "Created service-token SSM parameter %s (cloud-init publishes the token)\n", tokenParam)

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
		return c.abandonServiceToken(ctx, tokenParam, fmt.Errorf("creating security group: %w", err))
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
		return c.abandonServiceToken(ctx, tokenParam, fmt.Errorf("creating IAM role: %w", err))
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
		return c.abandonServiceToken(ctx, tokenParam, fmt.Errorf("creating instance profile: %w", err))
	}
	profileName := resources[len(resources)-1].Identifier

	// Create EC2 Instance
	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)
	genUserData := c.genUserData
	if genUserData == nil {
		genUserData = horde.Generate
	}
	userData, err := genUserData(horde.UserDataConfig{
		MongoPassword:         mongoPass,
		Port:                  plan.Port,
		ServiceTokenParameter: tokenParam,
		Region:                plan.Region,
	})
	if err != nil {
		return c.abandonServiceToken(ctx, tokenParam, fmt.Errorf("generating user data: %w", err))
	}

	// Track whether the instance itself was created: once it exists, the
	// parameter stays for its cloud-init and for destroy even if volume
	// tagging or the state write fails afterwards.
	instanceCreated := false
	createInstance := func(ctx context.Context, r *cloud.Resource) error {
		if err := c.createResource(ctx, r); err != nil {
			return err
		}
		instanceCreated = true
		return nil
	}

	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return horde.InstanceDesiredState(plan, sgID, userData, profileName)
		},
		// The service-token parameter is recorded on the instance, not as a
		// state resource: a drift Get of AWS::SSM::Parameter would return the
		// secret. Destroy, submit, and ci setup read the name from here.
		Properties: map[string]string{
			"instanceType":          plan.InstanceType,
			"volumeSize":            strconv.Itoa(plan.VolumeSize),
			"rootDevice":            plan.RootDeviceName,
			"serviceTokenParameter": tokenParam,
		},
		PostCreate: provision.TagVolumesPostCreate(c.runtime.Provider, "horde", plan.InstanceName),
		// fail on writeState error (default)
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, createInstance, c.writeState)
	if err != nil {
		wrapped := fmt.Errorf("creating EC2 instance: %w", err)
		if instanceCreated {
			return wrapped
		}
		return c.abandonServiceToken(ctx, tokenParam, wrapped)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

// abandonServiceToken deletes the placeholder SecureString after a create that
// failed before the instance existed, so the parameter does not outlive the
// attempt.
func (c command) abandonServiceToken(ctx context.Context, name string, err error) error {
	if delErr := c.deleteParameter(ctx, name); delErr != nil {
		return fmt.Errorf("%w (also failed to delete SSM parameter %s: %v)", err, name, delErr)
	}
	return err
}

func (c command) printDryRun(plan *horde.CreatePlan) {
	extraFields := []provision.PlanField{
		{Key: "AMI ID", Value: plan.AmiID},
		{Key: "HTTP port", Value: fmt.Sprintf("%d", plan.Port)},
		{Key: "gRPC port", Value: fmt.Sprintf("%d", plan.GRPCPort)},
		{Key: "Allowed CIDR", Value: plan.AllowedCIDR},
	}
	if plan.RootDeviceName != "" {
		extraFields = append(extraFields, provision.PlanField{Key: "Root volume", Value: plan.RootDeviceName + " (encrypted)"})
	}
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
		ExtraFields: extraFields,
		Resources: append([]string{
			"SSM Parameter:    " + plan.ServiceTokenParameter,
			"Security Group:   " + plan.SGName,
			"IAM Role:         " + plan.RoleName,
			"Instance Profile: " + plan.InstanceProfileName,
			"EC2 Instance:     " + plan.InstanceName,
		}, provision.IAMResourceLines(plan.IAM)...),
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
	}, append([]string{
		"SSM Parameter:    " + plan.ServiceTokenParameter,
		"Security Group:   " + plan.SGName,
		"IAM Role:         " + plan.RoleName,
		"Instance Profile: " + plan.InstanceProfileName,
		"EC2 Instance:     " + plan.InstanceName,
	}, provision.IAMResourceLines(plan.IAM)...))
}

func (c command) printPostCreate(plan *horde.CreatePlan, instanceID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Horde coordinator",
		InstanceID:   instanceID,
		StatusDetail: "provisioning (Horde starting up, ~3 min)",
		Details: []provision.PlanField{
			{Key: "Horde HTTP", Value: fmt.Sprintf("http://<private-ip>:%d", plan.Port)},
			{Key: "Horde gRPC", Value: fmt.Sprintf("<private-ip>:%d", plan.GRPCPort)},
			{Key: "Service token", Value: "SSM " + plan.ServiceTokenParameter},
		},
		NextSteps: []string{
			"fabrica horde status -w       Wait for coordinator to become ready",
			"Admin password                sudo cat /etc/horde/admin-password (over SSM)",
			"fabrica horde submit <file>   Submit a BuildGraph job (uses the service token)",
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
