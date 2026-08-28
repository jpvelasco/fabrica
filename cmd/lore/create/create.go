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
	"github.com/jpvelasco/fabrica/internal/oplog"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
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

Creates resources in order:
  1. EC2 Security Group — TCP 41337 (gRPC), UDP 41337 (QUIC), TCP 41339 (HTTP)
  2. S3 Store Bucket (optional) — enabled when lore.storeBackend is "s3"
  3. DynamoDB store tables (optional) — fragments, metadata, mutable, locks
     (required by the Lore 0.8.6 aws store plugin, s3 backend only)
  4. IAM Role + Instance Profile (optional) — S3 + DynamoDB access for the Lore instance
  5. EC2 Instance — runs loreserver using a user-provided AMI

Store backend:
  - "local" (default): EBS-backed store on the instance volume
  - "s3": S3 + DynamoDB-backed store (versioned bucket, four DynamoDB tables,
    and an IAM role with matching S3 + DynamoDB permissions)

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
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
	}

	loreCfg := c.runtime.Config.Lore
	if c.instanceType != "" {
		loreCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		loreCfg.VolumeSize = c.volumeSize
	}

	plan, err := lore.NewCreatePlan(ctx, loreCfg, account, region, provision.VPCResolver(c.runtime.Provider))
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	return provision.RunCreate(ctx, provision.CreateSpec[*lore.CreatePlan]{
		ModuleName:      moduleName,
		Account:         account,
		Plan:            plan,
		DryRun:          c.dryRun,
		AssumeYes:       c.assumeYes,
		Out:             c.out,
		ExistingMessage: "Lore is already provisioned. Run 'fabrica lore status' to check health.\nUse 'fabrica lore destroy' to remove it first.\n",
		Confirm:         c.confirm,
		ReadState:       c.readState,
		PrintDryRun:     c.printDryRun,
		PrintApplyPlan:  c.printApplyPlan,
		Apply:           c.applyCreate,
	})
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *lore.CreatePlan) error {
	if err := credentials.WriteCredentials(credFile, credentials.FormatLore(plan.GRPCPort, plan.HTTPPort)); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nConnection notes written to %s\n", credFile)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	var resources []fabricastate.ModuleResource
	resources, err := provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Security group",
		TypeName: cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) {
			return lore.SGDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

	// S3 store resources (bucket + IAM role + instance profile) — created before
	// the instance so the instance profile is available at launch.
	if plan.StoreBackend == lore.StoreBackendS3 {
		resources, err = c.createS3StoreResources(ctx, plan, resources, st)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)
	userData, err := lore.Generate(lore.UserDataConfig{
		StorePath:    lore.DefaultStorePath,
		ConfigDir:    lore.DefaultConfigDir,
		GRPCPort:     plan.GRPCPort,
		HTTPPort:     plan.HTTPPort,
		StoreBackend: plan.StoreBackend,
		StoreBucket:  plan.StoreBucket,
		StoreTables:  plan.StoreTables,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return lore.InstanceDesiredState(plan, sgID, userData)
		},
		Properties: map[string]string{
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
		PostCreate: provision.TagVolumesPostCreate(c.runtime.Provider, "lore", plan.InstanceName),
		// fail on writeState error (default)
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating EC2 instance: %w", err)
	}

	c.printPostCreate(plan, resources[len(resources)-1].Identifier)
	return nil
}

func (c command) createS3StoreResources(ctx context.Context, plan *lore.CreatePlan, resources []fabricastate.ModuleResource, st *fabricastate.State) ([]fabricastate.ModuleResource, error) {
	// S3 Bucket
	fmt.Fprintf(c.out, "Creating S3 store bucket %s...\n", plan.StoreBucket)
	oplog.WithModule("lore").Debug("creating S3 store bucket", "bucket", plan.StoreBucket)
	var err error
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "S3 store bucket",
		TypeName: cloud.TypeAWSS3Bucket,
		BuildDesiredState: func() ([]byte, error) {
			return lore.BucketDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return resources, fmt.Errorf("creating S3 store bucket: %w", err)
	}

	// DynamoDB store tables — required by the Lore 0.8.6 aws store plugin.
	// The plugin checks each table exists (DescribeTable) at startup, so all
	// four must exist before the instance launches.
	for _, spec := range lore.StoreTables() {
		suffix := spec.Suffix
		fmt.Fprintf(c.out, "Creating DynamoDB table %s...\n", plan.StoreBucket+"-"+suffix)
		oplog.WithModule("lore").Debug("creating DynamoDB store table", "table", plan.StoreBucket+"-"+suffix)
		resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
			Label:    "DynamoDB table",
			TypeName: cloud.TypeAWSDynamoDBTable,
			BuildDesiredState: func() ([]byte, error) {
				return lore.StoreTableDesiredState(plan, suffix)
			},
			ResourceIdentifier: func(*cloud.Resource) string { return plan.StoreBucket + "-" + suffix },
			Properties:         map[string]string{"loreTable": suffix},
		}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
		if err != nil {
			return resources, fmt.Errorf("creating DynamoDB table %s: %w", plan.StoreBucket+"-"+suffix, err)
		}
	}

	// IAM Role
	fmt.Fprintf(c.out, "Creating IAM role %s...\n", plan.RoleName)
	oplog.WithModule("lore").Debug("creating IAM role", "role", plan.RoleName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "IAM role",
		TypeName: cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) {
			return lore.RoleDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return resources, fmt.Errorf("creating IAM role: %w", err)
	}

	// IAM Instance Profile
	fmt.Fprintf(c.out, "Creating instance profile %s...\n", plan.InstanceProfileName)
	oplog.WithModule("lore").Debug("creating instance profile", "profile", plan.InstanceProfileName)
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "Instance profile",
		TypeName: cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) {
			return lore.InstanceProfileDesiredState(plan)
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return resources, fmt.Errorf("creating instance profile: %w", err)
	}

	return resources, nil
}

func (c command) printDryRun(plan *lore.CreatePlan) {
	resources := []string{
		"Security Group:   " + plan.SGName,
		"EC2 Instance:     " + plan.InstanceName,
	}
	if plan.StoreBackend == lore.StoreBackendS3 {
		resources = append(resources,
			"S3 Bucket:        "+plan.StoreBucket,
			"DynamoDB Tables:  "+strings.Join(plan.StoreTables, ", "),
			"IAM Role:         "+plan.RoleName,
			"Instance Profile: "+plan.InstanceProfileName,
		)
	}
	provision.DryRun(c.out, provision.DryRunSpec{
		Title: "Lore loreserver",
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
			{Key: "gRPC/QUIC port", Value: fmt.Sprintf("%d (tcp+udp)", plan.GRPCPort)},
			{Key: "HTTP port", Value: fmt.Sprintf("%d", plan.HTTPPort)},
			{Key: "Store backend", Value: plan.StoreBackend},
			{Key: "Allowed CIDR", Value: plan.AllowedCIDR},
		},
		Resources:     resources,
		CostResources: plan.CostResources,
		Costs:         c.costs,
		RawBetween: func(w io.Writer) {
			if plan.AllowedCIDR == "0.0.0.0/0" {
				fmt.Fprintln(w, "  Warning: allowedCidr is 0.0.0.0/0 — Lore ports are open")
				fmt.Fprintln(w, "           to the internet. Restrict this in fabrica.yaml before production use.")
			}
		},
	})
}

func (c command) printApplyPlan(plan *lore.CreatePlan) {
	resources := []string{
		"Security Group:   " + plan.SGName,
		"EC2 Instance:     " + plan.InstanceName,
	}
	if plan.StoreBackend == lore.StoreBackendS3 {
		resources = append(resources,
			"S3 Bucket:        "+plan.StoreBucket,
			"DynamoDB Tables:  "+strings.Join(plan.StoreTables, ", "),
			"IAM Role:         "+plan.RoleName,
			"Instance Profile: "+plan.InstanceProfileName,
		)
	}
	provision.ApplyPlan(c.out, "Lore loreserver", provision.PlanInfo{
		Account:      plan.Account,
		Region:       plan.Region,
		InstanceType: plan.InstanceType,
		VolumeSize:   plan.VolumeSize,
	}, []provision.PlanField{
		{Key: "AMI ID", Value: plan.AmiID},
		{Key: "Store backend", Value: plan.StoreBackend},
	}, resources)
}

func (c command) printPostCreate(plan *lore.CreatePlan, instanceID string) {
	provision.PostCreate(c.out, provision.PostCreateSpec{
		Title:        "Lore server",
		InstanceID:   instanceID,
		StatusDetail: "provisioning (loreserver starting up, ~3 min)",
		Details: []provision.PlanField{
			{Key: "gRPC/QUIC", Value: fmt.Sprintf("<private-ip>:%d (tcp+udp)", plan.GRPCPort)},
			{Key: "HTTP health", Value: fmt.Sprintf("http://<private-ip>:%d/health_check", plan.HTTPPort)},
			{Key: "Connection notes", Value: credFile},
		},
		NextSteps: []string{
			"fabrica lore status -w       Wait for server to become ready",
			"Point Lore clients at the private IP (see connection notes)",
		},
		RawAfter: func(w io.Writer) {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  Note: Lore is accessible via the instance's private IP. Ensure your")
			fmt.Fprintln(w, "        machine can reach it (VPN, VPC peering, or same-VPC access).")
			fmt.Fprintln(w, "        TLS is self-signed in V1; clients must trust the cert.")
			if plan.AllowedCIDR == "0.0.0.0/0" {
				fmt.Fprintln(w)
				fmt.Fprintln(w, "  Warning: lore.allowedCidr is 0.0.0.0/0 — ports are open to the internet.")
				fmt.Fprintln(w, "           Restrict this in fabrica.yaml before production use.")
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "If the server doesn't become ready within 10 minutes, check:")
			fmt.Fprintln(w, "  /var/log/fabrica-lore-init.log  on the instance")
		},
	})
}
