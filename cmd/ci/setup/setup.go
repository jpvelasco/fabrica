// Package setup implements "fabrica ci setup": provision the CI infrastructure
// (IAM role + CodeBuild project) that orchestrates Horde BuildGraph jobs.
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "ci"
	lineWidth  = 58
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	out       io.Writer
	costs     *fabricacost.Registry

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
	confirm        func(string) bool
}

// New returns the "ci setup" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision the CI infrastructure (CodeBuild project + IAM role)",
		Long: `Provision the Fabrica CI infrastructure for this account: an IAM service role
and a CodeBuild project that orchestrates Horde BuildGraph jobs.

The operation is idempotent: existing resources are detected and left in place.
You are asked to confirm before any resources are created; pass --yes to skip.
With --dry-run, it shows the planned resources and estimated monthly cost.`,
		Example: `  # Preview the plan and cost (no changes):
  fabrica ci setup --dry-run

  # Provision, confirming interactively:
  fabrica ci setup

  # Provision non-interactively (CI / automation):
  fabrica ci setup --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:    rt,
				dryRun:     opts.DryRun,
				assumeYes:  opts.AssumeYes,
				out:        out,
				costs:      fabricacost.Global,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.Confirm,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.createResource = rc.Create
					c.getResource = rc.Get
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}

	// Resolve the Horde URL from state if Horde is provisioned, so the project's
	// default HORDE_URL is populated. Not required for setup to succeed.
	hordeURL := c.resolveHordeURL(ctx)

	plan := ci.NewCreatePlan(c.runtime.Config.CI, account, region, hordeURL)

	if c.dryRun {
		c.printDryRun(plan)
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

	return c.apply(ctx, plan)
}

func (c command) apply(ctx context.Context, plan *ci.CreatePlan) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	resources, err := c.applyResources(ctx, st, plan)
	if err != nil {
		return err
	}

	st.UpsertModule(moduleName, plan.ProjectName, "ready", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	c.printCompletion()
	return nil
}

// applyResources creates the IAM role then the CodeBuild project, writing state
// after each so a partial failure is recoverable on re-run. Existing resources
// (detected in current state) are skipped.
func (c command) applyResources(ctx context.Context, st *fabricastate.State, plan *ci.CreatePlan) ([]fabricastate.ModuleResource, error) {
	var resources []fabricastate.ModuleResource

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", plan.Account, plan.RoleName)

	if existing, ok := existingResource(st, ci.TypeAWSIAMRole); ok {
		fmt.Fprintf(c.out, "  IAM role already exists — skipping: %s\n", existing.Identifier)
		resources = append(resources, existing)
	} else {
		roleState, err := ci.RoleDesiredState(plan)
		if err != nil {
			return nil, fmt.Errorf("building IAM role desired state: %w", err)
		}
		r := &cloud.Resource{TypeName: ci.TypeAWSIAMRole, DesiredState: roleState}
		if err := c.createResource(ctx, r); err != nil {
			return nil, fmt.Errorf("creating IAM role: %w", err)
		}
		fmt.Fprintf(c.out, "  created IAM role: %s\n", r.Identifier)
		resources = append(resources, fabricastate.ModuleResource{TypeName: ci.TypeAWSIAMRole, Identifier: r.Identifier})
		st.UpsertModule(moduleName, plan.ProjectName, "provisioning", resources)
		_ = c.writeState(st)
	}

	if existing, ok := existingResource(st, ci.TypeAWSCodeBuildProject); ok {
		fmt.Fprintf(c.out, "  CodeBuild project already exists — skipping: %s\n", existing.Identifier)
		resources = appendUnique(resources, existing)
		return resources, nil
	}

	projState, err := ci.ProjectDesiredState(plan, roleARN)
	if err != nil {
		return nil, fmt.Errorf("building CodeBuild project desired state: %w", err)
	}
	pr := &cloud.Resource{TypeName: ci.TypeAWSCodeBuildProject, DesiredState: projState}
	if err := c.createResource(ctx, pr); err != nil {
		return nil, fmt.Errorf("creating CodeBuild project: %w", err)
	}
	fmt.Fprintf(c.out, "  created CodeBuild project: %s\n", pr.Identifier)
	resources = append(resources, fabricastate.ModuleResource{TypeName: ci.TypeAWSCodeBuildProject, Identifier: pr.Identifier})
	return resources, nil
}

func appendUnique(resources []fabricastate.ModuleResource, r fabricastate.ModuleResource) []fabricastate.ModuleResource {
	for _, existing := range resources {
		if existing.TypeName == r.TypeName {
			return resources
		}
	}
	return append(resources, r)
}

func (c command) resolveHordeURL(ctx context.Context) string {
	st, err := c.readState()
	if err != nil {
		return ""
	}
	m := st.GetModule("horde")
	if m == nil {
		return ""
	}
	inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || inst.Identifier == "" || c.getResource == nil {
		return ""
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: inst.Identifier}
	if err := c.getResource(ctx, r); err != nil {
		return ""
	}
	ip := privateIPFromActualState(r.ActualState)
	if ip == "" {
		return ""
	}
	port := 5000
	if c.runtime.Config != nil && c.runtime.Config.Horde.Port > 0 {
		port = c.runtime.Config.Horde.Port
	}
	return fmt.Sprintf("http://%s:%d", ip, port)
}

func (c command) printDryRun(plan *ci.CreatePlan) {
	fmt.Fprintln(c.out, "CI setup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanDetails(plan)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to create these resources.")
}

func (c command) printPlan(plan *ci.CreatePlan) {
	fmt.Fprintln(c.out, "Setting up CI infrastructure...")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
}

func (c command) printPlanDetails(plan *ci.CreatePlan) {
	fmt.Fprintf(c.out, "  Account:       %s\n", plan.Account)
	fmt.Fprintf(c.out, "  Region:        %s\n", plan.Region)
	fmt.Fprintf(c.out, "  IAM role:      %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  CodeBuild:     %s (%s)\n", plan.ProjectName, plan.ComputeType)
	if plan.HordeURL != "" {
		fmt.Fprintf(c.out, "  Horde URL:     %s\n", plan.HordeURL)
	} else {
		fmt.Fprintln(c.out, "  Horde URL:     (not resolved — provision Horde, or it is set at trigger time)")
	}
	fmt.Fprintln(c.out)
}

func (c command) printCompletion() {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "CI setup complete.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintf(c.out, "  fabrica ci trigger <buildgraph.xml> --target <node>   Trigger a build\n")
	fmt.Fprintln(c.out, "  fabrica ci status                                     Show CI + build status")
}

// existingResource returns the CI module resource of the given type from current
// state, if present — used to skip already-provisioned resources idempotently.
func existingResource(st *fabricastate.State, typeName string) (fabricastate.ModuleResource, bool) {
	m := st.GetModule(moduleName)
	if m == nil {
		return fabricastate.ModuleResource{}, false
	}
	return stateutil.ResourceByType(m, typeName)
}

// privateIPFromActualState extracts PrivateIpAddress from Cloud Control JSON.
func privateIPFromActualState(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return ""
	}
	return actual.PrivateIPAddress
}
