// Package region implements `fabrica ddc region add`: provisioning an
// additional DDC edge node in a peer region, reusing the home stack's global
// IAM profile and shared blob bucket.
package region

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
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	moduleName = "ddc"
	lineWidth  = 58
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	out       io.Writer
	costs     *fabricacost.Registry
	confirm   func(string) bool
	opts      ddc.EdgeOptions

	readState  func() (*fabricastate.State, error)
	writeState func(*fabricastate.State) error
	withRegion func(ctx context.Context, region string) (cloud.RegionView, error)
}

// New returns the "ddc region" parent command with its "add" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	regionCmd := &cobra.Command{
		Use:   "region",
		Short: "Manage additional DDC regions",
		Long: `Manage additional DDC regions beyond the home region.

The home region is provisioned by 'fabrica ddc setup'. Additional regions add
edge nodes that share the home blob bucket and IAM profile.`,
	}
	regionCmd.AddCommand(newAdd(runtimeSource, optionsSource, out))
	return regionCmd
}

func newAdd(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var amiID, instanceType string
	var volumeSize int
	var vpcID, subnetID string

	cmd := &cobra.Command{
		Use:   "add REGION",
		Short: "Provision an additional DDC region",
		Long: `Provision one additional DDC edge node in REGION (e.g. eu-west-1).

  1. Security group (public + internal API ports) in REGION
  2. DDC EC2 instance (AMI-first) in REGION, reusing the home IAM profile
     and sharing the home blob bucket (hybrid EBS + S3)

The AMI must exist in REGION — copy the home AMI first:
  aws ec2 copy-image --source-region <home> --region REGION --name ddc-edge

Idempotent: re-running with an already-provisioned region exits cleanly.
With --dry-run, shows the plan and monthly cost estimate without AWS writes.`,
		Example: `  fabrica ddc region add eu-west-1 --dry-run
  fabrica ddc region add eu-west-1 --yes
  fabrica ddc region add eu-west-1 --ami-id ami-0edge --instance-type m7i.large`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: fabrica ddc region add REGION")
			}
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				out:       out,
				costs:     fabricacost.Global,
				confirm:   prompt.Confirm,
				opts: ddc.EdgeOptions{
					AmiID:        amiID,
					InstanceType: instanceType,
					VolumeSize:   volumeSize,
					VPCID:        vpcID,
					SubnetID:     subnetID,
				},
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			return c.run(cmd.Context(), args[0])
		},
	}
	cmd.Flags().StringVar(&amiID, "ami-id", "", "AMI for this region (defaults to ddc.amiId; must exist in the target region)")
	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (defaults to ddc.instanceType or m7i.xlarge)")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "Hot volume size in GiB (defaults to ddc.volumeSize or 500)")
	cmd.Flags().StringVar(&vpcID, "vpc-id", "", "VPC in the target region (defaults to the region's default VPC)")
	cmd.Flags().StringVar(&subnetID, "subnet-id", "", "Subnet in the target region (defaults to the region's default subnet)")
	return cmd
}

func (c command) run(ctx context.Context, region string) error {
	account, homeRegion, err := provision.ResolveIdentity(ctx, c.runtime.Provider)
	if err != nil {
		return err
	}

	if c.withRegion == nil {
		if rp, ok := c.runtime.Provider.(cloud.RegionProvider); ok {
			c.withRegion = rp.WithRegion
		}
	}
	if c.withRegion == nil {
		return fmt.Errorf("provider %q does not support additional regions", providerName(c.runtime.Provider))
	}
	view, err := c.withRegion(ctx, region)
	if err != nil {
		return fmt.Errorf("binding DDC region %s: %w", region, err)
	}

	plan, err := ddc.NewEdgePlan(ctx, c.runtime.Config.DDC, account, homeRegion, region, c.opts, view.VPCs)
	if err != nil {
		return fmt.Errorf("building edge plan: %w", err)
	}

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("DDC is not provisioned. Run 'fabrica ddc setup' in the home region first")
	}
	if ddc.EdgeInstanceExists(m.Resources, region) {
		fmt.Fprintf(c.out, "DDC region %s is already provisioned. Run 'fabrica ddc status' to check health.\n", region)
		return nil
	}

	c.printPlan(plan)
	if !provision.ConfirmSetup(c.out, provision.CreateResourcesPrompt, c.assumeYes, c.confirm) {
		return nil
	}

	return c.apply(ctx, st, m, region, view, plan)
}

func providerName(p cloud.Provider) string {
	if p == nil {
		return "<none>"
	}
	return p.Name()
}

// apply creates the edge SG (reusing it on resume), then the edge instance,
// persisting state after each step so a partial failure is recoverable.
func (c command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, region string, view cloud.RegionView, plan *ddc.EdgePlan) error {
	resources := m.Resources

	// Step 1: security group (find or create).
	sgID := ""
	if existing, ok := edgeSG(m.Resources, region); ok {
		sgID = existing.Identifier
		fmt.Fprintf(c.out, "  Security group already exists — resuming: %s\n", sgID)
	} else {
		ds, err := ddc.EdgeSGDesiredState(plan)
		if err != nil {
			return fmt.Errorf("building edge security group: %w", err)
		}
		res := &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, DesiredState: ds}
		if err := view.Resources.Create(ctx, res); err != nil {
			return fmt.Errorf("creating edge security group: %w", err)
		}
		sgID = res.Identifier
		fmt.Fprintf(c.out, "  Security group created: %s\n", sgID)
		resources = append(resources, fabricastate.ModuleResource{
			TypeName:   cloud.TypeAWSEC2SecurityGroup,
			Identifier: sgID,
			Properties: map[string]string{"region": region, "role": ddc.RoleEdge},
		})
		st.UpsertModule(moduleName, m.Version, m.Status, resources)
		if err := c.writeState(st); err != nil {
			return fmt.Errorf("writing state after edge security group: %w", err)
		}
	}

	// Step 2: DDC instance.
	ud, err := ddc.Generate(ddc.UserDataConfig{
		StorePath: ddc.DefaultStorePath, Bucket: plan.Bucket, Region: plan.Region,
		Namespace: plan.Namespace, PublicPort: plan.PublicPort, InternalPort: plan.InternalPort,
	})
	if err != nil {
		return fmt.Errorf("generating edge user data: %w", err)
	}
	ds, err := ddc.EdgeInstanceDesiredState(plan, sgID, ud)
	if err != nil {
		return fmt.Errorf("building edge instance: %w", err)
	}
	res := &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, DesiredState: ds}
	if err := view.Resources.Create(ctx, res); err != nil {
		return fmt.Errorf("creating DDC edge instance: %w", err)
	}
	fmt.Fprintf(c.out, "  DDC edge instance created: %s\n", res.Identifier)
	resources = append(resources, fabricastate.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Identifier: res.Identifier,
		Properties: map[string]string{
			"region":       region,
			"role":         ddc.RoleEdge,
			"instanceType": plan.InstanceType,
			"volumeSize":   strconv.Itoa(plan.VolumeSize),
		},
	})
	st.UpsertModule(moduleName, m.Version, m.Status, resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after edge instance: %w", err)
	}

	c.printCompletion(plan, res.Identifier)
	return nil
}

// edgeSG returns the recorded security group for an edge region, if present.
func edgeSG(resources []fabricastate.ModuleResource, region string) (fabricastate.ModuleResource, bool) {
	for _, r := range resources {
		if r.TypeName == cloud.TypeAWSEC2SecurityGroup && r.Properties != nil &&
			r.Properties["region"] == region && r.Properties["role"] == ddc.RoleEdge {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
}

func (c command) printDryRun(plan *ddc.EdgePlan) {
	fmt.Fprintln(c.out, "Additional DDC region (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanBody(plan)
	fmt.Fprintln(c.out)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printPlan(plan *ddc.EdgePlan) {
	fmt.Fprintln(c.out, "Additional DDC region")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanBody(plan)
}

func (c command) printPlanBody(plan *ddc.EdgePlan) {
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  New region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Hot volume:       %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintf(c.out, "  Blob bucket:      %s (shared with home)\n", plan.Bucket)
	fmt.Fprintf(c.out, "  Public port:      %d\n", plan.PublicPort)
	fmt.Fprintf(c.out, "  Allowed CIDR:     %s\n", plan.AllowedCIDR)
	if plan.VPCID != "" {
		fmt.Fprintf(c.out, "  VPC / subnet:     %s / %s\n", plan.VPCID, plan.SubnetID)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  DDC EC2:          %s (edge node)\n", plan.InstanceName)
	fmt.Fprintf(c.out, "  IAM profile:      %s (reused from home stack)\n", plan.InstanceProfileName)
	if w := ddc.WarnOpenCIDR(plan.AllowedCIDR); w != "" {
		fmt.Fprintln(c.out, w)
	}
}

func (c command) printCompletion(plan *ddc.EdgePlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "DDC edge region %s provisioned.\n", plan.Region)
	fmt.Fprintf(c.out, "  Instance ID:  %s\n", instanceID)
	fmt.Fprintln(c.out, "  Status:       provisioning (service starting, ~3–5 min)")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  1. Run 'fabrica ddc status' to list home + all edge regions")
	fmt.Fprintln(c.out, "     (edge health probes are not available in this release)")
	fmt.Fprintf(c.out, "  2. Point UE/Horde clients in %s at the edge's public URL\n", plan.Region)
	fmt.Fprintln(c.out, "     (see the security group's public port; VPN required for private IPs)")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  Note: replication between regions is operator-managed. Extend")
	fmt.Fprintln(c.out, "  ddc.internalCidr to cover both regions' VPCs to allow peer traffic,")
	fmt.Fprintln(c.out, "  and verify DDC record propagation on both sides.")
	if w := ddc.WarnOpenCIDR(plan.AllowedCIDR); w != "" {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, w)
	}
}
