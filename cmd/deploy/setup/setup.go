// Package setup implements "fabrica deploy setup": provision the deploy
// infrastructure (IAM role GameLift uses to read builds from S3 + a GameLift
// alias) that later promotes flip between fleets.
package setup

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "deploy"
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

// New returns the "deploy setup" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision deploy infrastructure (IAM role + GameLift alias)",
		Long: `Provision the Fabrica deploy infrastructure: an IAM role GameLift assumes to
read game-server builds from S3, and a GameLift alias used for blue/green
promotion. Idempotent; existing resources are detected and left in place.

Requires deploy.buildBucket in fabrica.yaml (where CI/Horde upload builds).

With --dry-run, shows the planned resources and estimated monthly cost.`,
		Example: `  fabrica deploy setup --dry-run
  fabrica deploy setup
  fabrica deploy setup --yes`,
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
	if c.runtime.Config.Deploy.BuildBucket == "" {
		return fmt.Errorf("deploy.buildBucket is not set in fabrica.yaml — set it to the S3 bucket where CI/Horde uploads server builds, then re-run")
	}
	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}

	plan := deploy.NewSetupPlan(c.runtime.Config.Deploy, account, region)
	plan.BuildBucket = c.runtime.Config.Deploy.BuildBucket

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

func (c command) apply(ctx context.Context, plan *deploy.SetupPlan) error {
	if c.createResource == nil {
		return fmt.Errorf("cloud provider does not support resource creation — only AWS is supported in V1")
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	var resources []fabricastate.ModuleResource

	if existing, ok := existingResource(st, deploy.TypeAWSIAMRole); ok {
		fmt.Fprintf(c.out, "  IAM role already exists — skipping: %s\n", existing.Identifier)
		resources = append(resources, existing)
	} else {
		roleState, err := deploy.RoleDesiredState(plan)
		if err != nil {
			return fmt.Errorf("building IAM role desired state: %w", err)
		}
		r := &cloud.Resource{TypeName: deploy.TypeAWSIAMRole, DesiredState: roleState}
		if err := c.createResource(ctx, r); err != nil {
			return fmt.Errorf("creating IAM role: %w", err)
		}
		fmt.Fprintf(c.out, "  created IAM role: %s\n", r.Identifier)
		resources = append(resources, fabricastate.ModuleResource{TypeName: deploy.TypeAWSIAMRole, Identifier: r.Identifier})
		st.UpsertModule(moduleName, plan.AliasName, "provisioning", resources)
		_ = c.writeState(st)
	}

	if existing, ok := existingResource(st, deploy.TypeGameLiftAlias); ok {
		fmt.Fprintf(c.out, "  alias already exists — skipping: %s\n", existing.Identifier)
		resources = appendUnique(resources, existing)
	} else {
		aliasState, err := deploy.AliasDesiredState(plan)
		if err != nil {
			return fmt.Errorf("building alias desired state: %w", err)
		}
		r := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, DesiredState: aliasState}
		if err := c.createResource(ctx, r); err != nil {
			return fmt.Errorf("creating GameLift alias: %w", err)
		}
		fmt.Fprintf(c.out, "  created GameLift alias: %s\n", r.Identifier)
		resources = appendUnique(resources, fabricastate.ModuleResource{TypeName: deploy.TypeGameLiftAlias, Identifier: r.Identifier})
	}

	st.UpsertModule(moduleName, plan.AliasName, "ready", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	c.printCompletion()
	return nil
}

func appendUnique(resources []fabricastate.ModuleResource, r fabricastate.ModuleResource) []fabricastate.ModuleResource {
	for _, e := range resources {
		if e.TypeName == r.TypeName {
			return resources
		}
	}
	return append(resources, r)
}

func existingResource(st *fabricastate.State, typeName string) (fabricastate.ModuleResource, bool) {
	m := st.GetModule(moduleName)
	if m == nil {
		return fabricastate.ModuleResource{}, false
	}
	return stateutil.ResourceByType(m, typeName)
}

func (c command) printDryRun(plan *deploy.SetupPlan) {
	fmt.Fprintln(c.out, "Deploy setup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanDetails(plan)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to create these resources.")
}

func (c command) printPlan(plan *deploy.SetupPlan) {
	fmt.Fprintln(c.out, "Setting up deploy infrastructure...")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
}

func (c command) printPlanDetails(plan *deploy.SetupPlan) {
	fmt.Fprintf(c.out, "  Account:       %s\n", plan.Account)
	fmt.Fprintf(c.out, "  Region:        %s\n", plan.Region)
	fmt.Fprintf(c.out, "  IAM role:      %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  GameLift alias: %s\n", plan.AliasName)
	fmt.Fprintf(c.out, "  Build bucket:  %s\n", plan.BuildBucket)
	fmt.Fprintln(c.out)
}

func (c command) printCompletion() {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Deploy setup complete.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica deploy promote <build-version>   Roll out a build to a new fleet")
	fmt.Fprintln(c.out, "  fabrica deploy status                    Show deploy status")
}
