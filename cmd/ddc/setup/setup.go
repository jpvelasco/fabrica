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
	account, region, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
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

	// IAM Role
	resources, err := provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "IAM role",
		TypeName:          cloud.TypeAWSIAMRole,
		BuildDesiredState: func() ([]byte, error) { return ddc.RoleDesiredState(plan) },
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating IAM role: %w", err)
	}

	// Instance Profile
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Instance profile",
		TypeName:          cloud.TypeAWSIAMInstanceProfile,
		BuildDesiredState: func() ([]byte, error) { return ddc.InstanceProfileDesiredState(plan) },
		IgnoreWriteError:  true,
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating instance profile: %w", err)
	}

	// S3 Bucket
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "S3 bucket",
		TypeName:          ddc.TypeAWSS3Bucket,
		BuildDesiredState: func() ([]byte, error) { return ddc.BucketDesiredState(plan) },
		Properties:        map[string]string{"region": plan.Region, "role": ddc.RoleBlob},
		IgnoreWriteError:  true,
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating S3 bucket: %w", err)
	}

	// Security Group
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:             "Security group",
		TypeName:          cloud.TypeAWSEC2SecurityGroup,
		BuildDesiredState: func() ([]byte, error) { return ddc.SGDesiredState(plan) },
		IgnoreWriteError:  true,
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	sgID := resources[len(resources)-1].Identifier

	// DDC Instance
	ud, err := ddc.Generate(ddc.UserDataConfig{
		StorePath: ddc.DefaultStorePath, Bucket: plan.Bucket, Region: plan.Region,
		Namespace: plan.Namespace, PublicPort: plan.PublicPort, InternalPort: plan.InternalPort,
		Backend: plan.Backend,
	})
	if err != nil {
		return fmt.Errorf("generating ddc user data: %w", err)
	}
	resources, err = provision.ExecuteStep(ctx, provision.CreateStep{
		Label:    "DDC instance",
		TypeName: cloud.TypeAWSEC2Instance,
		BuildDesiredState: func() ([]byte, error) {
			return ddc.InstanceDesiredState(plan, sgID, ud, plan.InstanceProfileName)
		},
		Properties: map[string]string{
			"region":       plan.Region,
			"role":         ddc.RoleCoordinator,
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
	}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState)
	if err != nil {
		return fmt.Errorf("creating DDC instance: %w", err)
	}
	instID := resources[len(resources)-1].Identifier

	// Optional Scylla Instance
	if plan.Backend == ddc.BackendScylla {
		scyllaUD, err := ddc.GenerateScylla(ddc.ScyllaUserDataConfig{ClusterName: "fabrica-ddc"})
		if err != nil {
			return fmt.Errorf("generating scylla user data: %w", err)
		}
		if _, err = provision.ExecuteStep(ctx, provision.CreateStep{
			Label:    "Scylla bootstrap instance",
			TypeName: cloud.TypeAWSEC2Instance,
			BuildDesiredState: func() ([]byte, error) {
				return ddc.ScyllaInstanceDesiredState(plan, sgID, scyllaUD, plan.InstanceProfileName)
			},
			Properties: map[string]string{
				"region":       plan.Region,
				"role":         ddc.RoleScylla,
				"instanceType": plan.ScyllaInstanceType,
				"volumeSize":   strconv.Itoa(plan.ScyllaVolumeSize),
			},
			IgnoreWriteError: true,
		}, moduleName, plan.AmiID, "provisioning", resources, st, c.out, c.createResource, c.writeState); err != nil {
			return fmt.Errorf("creating Scylla instance: %w", err)
		}
	}

	if err := c.writeEndpointsFile(plan); err != nil {
		return err
	}
	c.printCompletion(plan, instID)
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
