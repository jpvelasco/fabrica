package setup

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
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	moduleName    = "ddc"
	lineWidth     = 58
	endpointsFile = ".fabrica/ddc-endpoints.yaml"
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	backend   string
	out       io.Writer
	costs     *fabricacost.Registry
	confirm   func(string) bool

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	writeEndpoints func(path, content string) error
}

// New returns the "ddc setup" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var backend string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Provision Unreal Cloud DDC (single home-region)",
		Long: `Provision a single home-region Unreal Cloud DDC (Jupiter) stack:

  1. IAM role + instance profile (S3 RW on the DDC bucket)
  2. S3 bucket for durable blobs
  3. Security group (public + internal API ports)
  4. Optional: 1-node Scylla EC2 when --backend scylla (NOT production HA)
  5. DDC EC2 instance (AMI-first) with hybrid EBS hot store

Default backend is zen. Scylla is an advanced single-node bootstrap path only.

Idempotent: if the ddc module already exists, setup exits cleanly.
With --dry-run, shows the plan and monthly cost estimate without AWS writes.`,
		Example: `  fabrica ddc setup --dry-run
  fabrica ddc setup --yes
  fabrica ddc setup --backend scylla --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				backend:   backend,
				out:       out,
				costs:     fabricacost.Global,
				confirm:   prompt.Confirm,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			c.writeEndpoints = credentials.WriteCredentials
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&backend, "backend", "", "zen (default) or scylla (optional 1-node bootstrap, not HA)")
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

	cfg := c.runtime.Config.DDC
	if c.backend != "" {
		cfg.Backend = c.backend
	}

	var resolver cloud.VPCResolver
	if vr, ok := c.runtime.Provider.(cloud.VPCResolver); ok {
		resolver = vr
	}
	plan, err := ddc.NewSetupPlan(ctx, cfg, account, region, resolver)
	if err != nil {
		return fmt.Errorf("building setup plan: %w", err)
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
		fmt.Fprintln(c.out, "DDC is already provisioned. Run 'fabrica ddc status' to check health.")
		fmt.Fprintln(c.out, "Use 'fabrica ddc destroy' to remove it first.")
		return nil
	}

	c.printPlan(plan)
	if !c.assumeYes {
		if !c.confirm("Create these resources?") {
			fmt.Fprintln(c.out, "Setup cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
	}

	return c.apply(ctx, st, plan)
}

func (c command) apply(ctx context.Context, st *fabricastate.State, plan *ddc.SetupPlan) error {
	var resources []fabricastate.ModuleResource

	if err := c.createIAMRole(ctx, plan, &resources, st); err != nil {
		return err
	}
	if err := c.createInstanceProfile(ctx, plan, &resources, st); err != nil {
		return err
	}
	_, err := c.createS3Bucket(ctx, plan, &resources, st)
	if err != nil {
		return err
	}
	sgID, err := c.createSecurityGroup(ctx, plan, &resources, st)
	if err != nil {
		return err
	}
	instID, profileName, err := c.createDDCInstance(ctx, plan, sgID, &resources, st)
	if err != nil {
		return err
	}
	if plan.Backend == ddc.BackendScylla {
		if err := c.createScyllaInstance(ctx, plan, sgID, profileName, &resources, st); err != nil {
			return err
		}
	}

	if err := c.writeEndpointsFile(plan); err != nil {
		return err
	}
	c.printCompletion(plan, instID)
	return nil
}

func (c command) createIAMRole(ctx context.Context, plan *ddc.SetupPlan, resources *[]fabricastate.ModuleResource, st *fabricastate.State) error {
	roleState, err := ddc.RoleDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building IAM role desired state: %w", err)
	}
	role := &cloud.Resource{TypeName: ddc.TypeAWSIAMRole, DesiredState: roleState}
	if err := c.createResource(ctx, role); err != nil {
		return fmt.Errorf("creating IAM role: %w", err)
	}
	fmt.Fprintf(c.out, "  created IAM role: %s\n", role.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{TypeName: ddc.TypeAWSIAMRole, Identifier: role.Identifier})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	return nil
}

func (c command) createInstanceProfile(ctx context.Context, plan *ddc.SetupPlan, resources *[]fabricastate.ModuleResource, st *fabricastate.State) error {
	profState, err := ddc.InstanceProfileDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building instance profile desired state: %w", err)
	}
	prof := &cloud.Resource{TypeName: ddc.TypeAWSIAMInstanceProfile, DesiredState: profState}
	if err := c.createResource(ctx, prof); err != nil {
		return fmt.Errorf("creating instance profile: %w", err)
	}
	fmt.Fprintf(c.out, "  created instance profile: %s\n", prof.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{TypeName: ddc.TypeAWSIAMInstanceProfile, Identifier: prof.Identifier})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	_ = c.writeState(st)
	return nil
}

func (c command) createS3Bucket(ctx context.Context, plan *ddc.SetupPlan, resources *[]fabricastate.ModuleResource, st *fabricastate.State) (string, error) {
	bucketState, err := ddc.BucketDesiredState(plan)
	if err != nil {
		return "", fmt.Errorf("building bucket desired state: %w", err)
	}
	bucket := &cloud.Resource{TypeName: ddc.TypeAWSS3Bucket, DesiredState: bucketState}
	if err := c.createResource(ctx, bucket); err != nil {
		return "", fmt.Errorf("creating S3 bucket: %w", err)
	}
	fmt.Fprintf(c.out, "  created S3 bucket: %s\n", bucket.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{
		TypeName: ddc.TypeAWSS3Bucket, Identifier: bucket.Identifier,
		Properties: map[string]string{"region": plan.Region, "role": ddc.RoleBlob},
	})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	_ = c.writeState(st)
	return bucket.Identifier, nil
}

func (c command) createSecurityGroup(ctx context.Context, plan *ddc.SetupPlan, resources *[]fabricastate.ModuleResource, st *fabricastate.State) (string, error) {
	sgState, err := ddc.SGDesiredState(plan)
	if err != nil {
		return "", fmt.Errorf("building SG desired state: %w", err)
	}
	sg := &cloud.Resource{TypeName: ddc.TypeAWSEC2SecurityGroup, DesiredState: sgState}
	if err := c.createResource(ctx, sg); err != nil {
		return "", fmt.Errorf("creating security group: %w", err)
	}
	fmt.Fprintf(c.out, "  created security group: %s\n", sg.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{TypeName: ddc.TypeAWSEC2SecurityGroup, Identifier: sg.Identifier})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	_ = c.writeState(st)
	return sg.Identifier, nil
}

func (c command) createDDCInstance(ctx context.Context, plan *ddc.SetupPlan, sgID string, resources *[]fabricastate.ModuleResource, st *fabricastate.State) (string, string, error) {
	ud, err := ddc.Generate(ddc.UserDataConfig{
		StorePath: ddc.DefaultStorePath, Bucket: plan.Bucket, Region: plan.Region,
		Namespace: plan.Namespace, PublicPort: plan.PublicPort, InternalPort: plan.InternalPort,
		Backend: plan.Backend,
	})
	if err != nil {
		return "", "", fmt.Errorf("generating ddc user data: %w", err)
	}
	profileName := plan.InstanceProfileName
	instState, err := ddc.InstanceDesiredState(plan, sgID, ud, profileName)
	if err != nil {
		return "", "", fmt.Errorf("building ddc instance desired state: %w", err)
	}
	inst := &cloud.Resource{TypeName: ddc.TypeAWSEC2Instance, DesiredState: instState}
	if err := c.createResource(ctx, inst); err != nil {
		return "", "", fmt.Errorf("creating ddc instance: %w", err)
	}
	fmt.Fprintf(c.out, "  created DDC instance: %s\n", inst.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{
		TypeName: ddc.TypeAWSEC2Instance, Identifier: inst.Identifier,
		Properties: map[string]string{
			"region": plan.Region, "role": ddc.RoleCoordinator,
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
	})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	if err := c.writeState(st); err != nil {
		return "", "", fmt.Errorf("writing state: %w", err)
	}
	return inst.Identifier, profileName, nil
}

func (c command) createScyllaInstance(ctx context.Context, plan *ddc.SetupPlan, sgID, profileName string, resources *[]fabricastate.ModuleResource, st *fabricastate.State) error {
	scyllaUD, err := ddc.GenerateScylla(ddc.ScyllaUserDataConfig{ClusterName: "fabrica-ddc"})
	if err != nil {
		return fmt.Errorf("generating scylla user data: %w", err)
	}
	scyllaState, err := ddc.ScyllaInstanceDesiredState(plan, sgID, scyllaUD, profileName)
	if err != nil {
		return fmt.Errorf("building scylla instance desired state: %w", err)
	}
	scylla := &cloud.Resource{TypeName: ddc.TypeAWSEC2Instance, DesiredState: scyllaState}
	if err := c.createResource(ctx, scylla); err != nil {
		return fmt.Errorf("creating scylla instance: %w", err)
	}
	fmt.Fprintf(c.out, "  created Scylla bootstrap instance: %s\n", scylla.Identifier)
	*resources = append(*resources, fabricastate.ModuleResource{
		TypeName: ddc.TypeAWSEC2Instance, Identifier: scylla.Identifier,
		Properties: map[string]string{
			"region": plan.Region, "role": ddc.RoleScylla,
			"instanceType": plan.ScyllaInstanceType,
			"volumeSize":   strconv.Itoa(plan.ScyllaVolumeSize),
		},
	})
	st.UpsertModule(moduleName, plan.AmiID, "provisioning", *resources)
	_ = c.writeState(st)
	return nil
}

func (c command) writeEndpointsFile(plan *ddc.SetupPlan) error {
	ep := ddc.FormatEndpointsYAML(ddc.Endpoints{
		Backend: plan.Backend, Namespace: plan.Namespace, Region: plan.Region, Bucket: plan.Bucket,
		PublicURL:   fmt.Sprintf("http://<private-ip>:%d", plan.PublicPort),
		InternalURL: fmt.Sprintf("http://<private-ip>:%d", plan.InternalPort),
	})
	if err := c.writeEndpoints(endpointsFile, ep); err != nil {
		return fmt.Errorf("writing endpoints file: %w", err)
	}
	fmt.Fprintf(c.out, "  endpoints written to %s\n", endpointsFile)
	return nil
}

func (c command) printDryRun(plan *ddc.SetupPlan) {
	fmt.Fprintln(c.out, "Distributed DDC (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanBody(plan)
	fmt.Fprintln(c.out)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printPlan(plan *ddc.SetupPlan) {
	fmt.Fprintln(c.out, "Distributed DDC")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanBody(plan)
}

func (c command) printPlanBody(plan *ddc.SetupPlan) {
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s (home only — no multi-region in V1)\n", plan.Region)
	fmt.Fprintf(c.out, "  Backend:          %s\n", plan.Backend)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Hot volume:       %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintf(c.out, "  Blob bucket:      %s\n", plan.Bucket)
	fmt.Fprintf(c.out, "  Namespace:        %s\n", plan.Namespace)
	fmt.Fprintf(c.out, "  Public port:      %d\n", plan.PublicPort)
	fmt.Fprintf(c.out, "  Allowed CIDR:     %s\n", plan.AllowedCIDR)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  IAM role:         %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  Instance profile: %s\n", plan.InstanceProfileName)
	fmt.Fprintf(c.out, "  S3 bucket:        %s\n", plan.Bucket)
	fmt.Fprintf(c.out, "  Security group:   %s\n", plan.SGName)
	if plan.Backend == ddc.BackendScylla {
		fmt.Fprintf(c.out, "  Scylla EC2:       %s (%s) — bootstrap only\n", plan.ScyllaInstanceName, plan.ScyllaInstanceType)
	}
	fmt.Fprintf(c.out, "  DDC EC2:          %s (coordinator+edge co-located)\n", plan.InstanceName)
	if w := ddc.WarnOpenCIDR(plan.AllowedCIDR); w != "" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, w)
	}
	if plan.Backend == ddc.BackendScylla {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ddc.WarnScyllaBootstrap())
	}
}

func (c command) printCompletion(plan *ddc.SetupPlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "DDC provisioned.")
	fmt.Fprintf(c.out, "  Instance ID:  %s\n", instanceID)
	fmt.Fprintln(c.out, "  Status:       provisioning (service starting, ~3–5 min)")
	fmt.Fprintf(c.out, "  Endpoints:    %s\n", endpointsFile)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  1. fabrica ddc status --probe   Wait for /health/ready")
	fmt.Fprintf(c.out, "  2. Point UE/Horde cooks at http://<private-ip>:%d\n", plan.PublicPort)
	fmt.Fprintln(c.out, "     e.g. -UE-CloudDataCacheHost=http://<private-ip>")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  Note: V1 is single home-region only. No region add in this release.")
	if w := ddc.WarnOpenCIDR(plan.AllowedCIDR); w != "" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, w)
	}
}
