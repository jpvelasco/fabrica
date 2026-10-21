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
	"github.com/jpvelasco/fabrica/internal/config"
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
	inspectImage   func(ctx context.Context, imageID string) (cloud.ImageInfo, error)

	putSecureParameter func(ctx context.Context, p cloud.SecureParameter) error
	deleteParameter    func(ctx context.Context, name string) error
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

Creates four Cloud Control resources in order:
  1. EC2 Security Group — allows TCP 8443 inbound (NICE DCV HTTPS)
  2. IAM Role — AmazonSSMManagedInstanceCore, fabrica-ssm-output, and ssm:GetParameter on the session-password parameter only
  3. IAM Instance Profile — wraps the role for EC2 attachment
  4. EC2 Instance — runs NICE DCV from the provided AMI (attached to the instance profile for SSM access)

The DCV session password is a Parameter Store SecureString, not a fifth
Cloud Control resource and not a UserData field. Each create stores it under a
per-create name /fabrica/workstation/<create-nonce>/dcv-password, so a
concurrent create cannot clobber another instance's password. Cloud-init
reads it with aws ssm get-parameter; the instance role may ssm:GetParameter
on that parameter ARN only. .fabrica/workstation-credentials.yaml stays the
operator source of truth. The name is recorded in state on the instance, and
terminate deletes that recorded name. A create that fails before the instance
exists deletes the parameter too; once the instance exists the parameter stays
so cloud-init can read it.

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

	rootDevice, err := c.inspectAMI(ctx, wsCfg.AmiID)
	if err != nil {
		return err
	}
	if rootDevice == "" && !c.dryRun {
		// Without the AMI's own root device there is no way to put the
		// encrypted mapping on the real root; a guessed device would boot the
		// root unencrypted from the snapshot (#507). Fail before any write.
		return fmt.Errorf("cannot read the root device of workstation.amiId %s (the provider cannot inspect AMIs), so the root volume cannot be encrypted; refusing to launch", wsCfg.AmiID)
	}

	resolvedIAM, err := config.ResolveIAM(c.runtime.Config.Cloud.AWS.IAM, wsCfg.IAM)
	if err != nil {
		return err
	}
	wsCfg.IAM = resolvedIAM

	plan, err := workstation.NewCreatePlan(ctx, wsCfg, account, region, provision.VPCResolver(c.runtime.Provider), c.template, perforceAddr)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}
	plan.RootDeviceName = rootDevice

	// Mint the per-create session-password parameter name before any plan is
	// printed or applied so the dry-run/apply previews name the same
	// SecureString apply creates. A per-create nonce (not a shared name) keeps
	// concurrent creates from clobbering one another's password and lets
	// terminate delete exactly this name.
	nonce, err := workstation.NewSessionPasswordNonce()
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}
	if plan.SessionPasswordParameter, err = workstation.SessionPasswordParameterFor(nonce); err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	// Mint the per-create identity nonce before any plan is printed or
	// applied. It is stamped on the instance as the FabricaCreateNonce tag at
	// launch, narrows the SDK launch path's adoption lookup and keys its
	// RunInstances ClientToken (fresh per invocation, so a re-create after
	// terminate cannot reuse the previous create's idempotency), and is
	// recorded in state (Properties "createNonce").
	if plan.CreateNonce, err = workstation.NewCreateNonce(); err != nil {
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
		Runtime:         c.runtime,
		Operation:       "workstation create",
	})
}

// inspectAMI is the create-time AMI contract check. It rejects stock Ubuntu
// images and returns the AMI's own root device, validated by
// workstation.RootDeviceFor (fail closed on a missing or malformed name, or a
// non-EBS root), so the encrypted root mapping lands on the real root (#507).
// A missing amiId is reported as a config error. It returns "" with no error
// only when the provider cannot inspect; run refuses to apply in that case.
func (c command) inspectAMI(ctx context.Context, amiID string) (string, error) {
	if amiID == "" {
		return "", fmt.Errorf("workstation.amiId is required. Provide a NICE DCV-enabled AMI ID.\nSee: https://docs.aws.amazon.com/dcv/latest/adminguide/setting-up-installing.html")
	}
	if c.inspectImage == nil {
		return "", nil
	}
	info, err := c.inspectImage(ctx, amiID)
	if err != nil {
		return "", fmt.Errorf("inspecting workstation.amiId %s: %w. Confirm the AMI exists and is a NICE DCV image (see docs/workstation-ami.md)", amiID, err)
	}
	if workstation.IsStockUbuntuImage(info.Name) {
		return "", fmt.Errorf("workstation.amiId %s is a stock Ubuntu image (%q), not a NICE DCV AMI. Bake one with 'fabrica workstation ami build' and set workstation.amiId to the result. See docs/workstation-ami.md", amiID, info.Name)
	}
	return workstation.RootDeviceFor(amiID, info.RootDeviceName, info.RootDeviceType)
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
	if c.putSecureParameter == nil || c.deleteParameter == nil {
		return fmt.Errorf("storing the DCV session password requires a provider with SSM parameter support. Run 'fabrica setup' with the AWS provider.")
	}

	sessionPass, err := credentials.GeneratePassword(passwordLen)
	if err != nil {
		return fmt.Errorf("generating session password: %w", err)
	}

	credContent := credentials.FormatWorkstation(sessionPass)
	if err := credentials.WriteCredentials(credFile, credContent); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nDCV credentials written to %s\n", credFile)

	// plan.SessionPasswordParameter is minted in run() before the plan is
	// printed or applied, so the role (which scopes ssm:GetParameter to this
	// ARN) and cloud-init (which reads this name at boot) and the state record
	// all agree on one per-create name. A shared name would let a concurrent
	// create or a re-create clobber another instance's password.
	paramName := plan.SessionPasswordParameter
	if err := c.putSecureParameter(ctx, cloud.SecureParameter{
		Name:  paramName,
		Value: sessionPass,
		Tags:  map[string]string{"ManagedBy": "fabrica"},
	}); err != nil {
		return fmt.Errorf("storing the DCV session password in SSM parameter %s: %w", paramName, err)
	}
	fmt.Fprintf(c.out, "Stored DCV session password in SSM parameter %s\n", paramName)

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
		return c.abandonSessionPassword(ctx, paramName, fmt.Errorf("creating security group: %w", err))
	}
	sgID := resources[len(resources)-1].Identifier

	fmt.Fprintf(c.out, "Creating IAM role %s...\n", plan.RoleName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "IAM role",
		TypeName: cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) {
			return workstation.RoleDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return c.abandonSessionPassword(ctx, paramName, fmt.Errorf("creating IAM role: %w", err))
	}

	fmt.Fprintf(c.out, "Creating instance profile %s...\n", plan.InstanceProfileName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance profile",
		TypeName: cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) {
			return workstation.InstanceProfileDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return c.abandonSessionPassword(ctx, paramName, fmt.Errorf("creating instance profile: %w", err))
	}

	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)
	userData, err := workstation.Generate(workstation.UserDataConfig{
		SessionPasswordParameter: paramName,
		Region:                   plan.Region,
		IdleTimeoutMinutes:       plan.IdleTimeoutMinutes,
		MountPerforce:            plan.MountPerforce,
		PerforceServerAddr:       plan.PerforceServerAddr,
	})
	if err != nil {
		return c.abandonSessionPassword(ctx, paramName, fmt.Errorf("generating user data: %w", err))
	}

	// Set only when Create itself returns nil. ExecuteStep also fails when the
	// following state write fails, and in that case the instance already exists.
	instanceCreated := false
	createInstance := func(ctx context.Context, r *cloud.Resource) error {
		if err := c.createResource(ctx, r); err != nil {
			return err
		}
		instanceCreated = true
		return nil
	}

	// The per-create parameter name and identity nonce are recorded in state
	// on the instance (Properties "sessionPasswordParameter" and
	// "createNonce"), not as state resources — a drift Get of
	// AWS::SSM::Parameter would return the secret. Terminate deletes the
	// recorded name, and a failed create deletes it here.
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return workstation.InstanceDesiredState(plan, sgID, userData)
		},
		Properties: map[string]string{
			"instanceType":             plan.InstanceType,
			"volumeSize":               strconv.Itoa(plan.VolumeSize),
			"sessionPasswordParameter": paramName,
			"createNonce":              plan.CreateNonce,
		},
		PostCreate: provision.TagVolumesPostCreate(c.runtime.Provider, "workstation", plan.InstanceName),
		// fail on writeState error (default)
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, createInstance, c.writeState)
	if err != nil {
		wrapped := fmt.Errorf("creating EC2 instance: %w", err)
		// The instance exists (volume tagging or the state write failed) and
		// cloud-init still has to read the password. Terminate deletes it.
		// Every earlier failure deletes it now.
		if instanceCreated {
			return wrapped
		}
		return c.abandonSessionPassword(ctx, paramName, wrapped)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

// abandonSessionPassword deletes the SecureString after a failed create so
// the secret does not outlive the attempt. It is called only on pre-instance
// steps; once the instance exists, create returns the error directly and the
// parameter stays for the instance's cloud-init and for terminate. The local
// credentials file is left in place; it is the operator record, and the next
// create overwrites it.
func (c command) abandonSessionPassword(ctx context.Context, name string, err error) error {
	if c.deleteParameter == nil {
		return fmt.Errorf("%w (SSM parameter %s was not deleted: no parameter store)", err, name)
	}
	if delErr := c.deleteParameter(ctx, name); delErr != nil {
		return fmt.Errorf("%w (also failed to delete SSM parameter %s: %v)", err, name, delErr)
	}
	return err
}

func (c command) printDryRun(plan *workstation.CreatePlan) {
	extraFields := []provision.PlanField{
		{Key: "AMI ID", Value: plan.AmiID},
		{Key: "Idle timeout", Value: fmt.Sprintf("%d min", plan.IdleTimeoutMinutes)},
	}
	if plan.RootDeviceName != "" {
		extraFields = append(extraFields, provision.PlanField{Key: "Root volume", Value: plan.RootDeviceName + " (encrypted)"})
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
		Resources: append([]string{
			"SSM Parameter:    " + plan.SessionPasswordParameter,
			"Security Group:   " + plan.SGName,
			"IAM Role:         " + plan.RoleName,
			"Instance Profile: " + plan.InstanceProfileName,
			"EC2 Instance:     " + plan.InstanceName,
		}, provision.IAMResourceLines(plan.IAM)...),
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
		Resources: append([]string{
			// Compact resource labels match pre-#162 workstation apply.
			"SSM Parameter:    " + plan.SessionPasswordParameter,
			"Security Group:   " + plan.SGName,
			"IAM Role:         " + plan.RoleName,
			"Instance Profile: " + plan.InstanceProfileName,
			"EC2 Instance:     " + plan.InstanceName,
		}, provision.IAMResourceLines(plan.IAM)...),
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
