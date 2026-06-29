// Package promote implements "fabrica deploy promote <build-version>": register
// a server build from S3, create a new GameLift fleet for it, wait for the fleet
// to reach ACTIVE, then flip the alias to it (blue/green). The previous fleet is
// retained for rollback.
package promote

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

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
	moduleName   = "deploy"
	lineWidth    = 58
	pollInterval = 20 * time.Second
)

type command struct {
	runtime      globals.Runtime
	buildVersion string
	s3Bucket     string
	s3Key        string
	wait         bool
	dryRun       bool
	assumeYes    bool
	out          io.Writer
	costs        *fabricacost.Registry

	readState        func() (*fabricastate.State, error)
	writeState       func(*fabricastate.State) error
	createResource   func(ctx context.Context, r *cloud.Resource) error
	updateResource   func(ctx context.Context, r *cloud.Resource) error
	getResource      func(ctx context.Context, r *cloud.Resource) error
	createFleetAsync func(ctx context.Context, r *cloud.Resource) error
	fleetStatus      func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	fleetEvents      func(ctx context.Context, fleetID string) ([]cloud.FleetEvent, error)
	confirm          func(string) bool
	sleep            func(time.Duration)
	now              func() time.Time
}

// New returns the "deploy promote" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var s3Bucket, s3Key string
	var noWait bool
	cmd := &cobra.Command{
		Use:   "promote <build-version>",
		Short: "Register a build and roll it out to a new GameLift fleet (blue/green)",
		Long: `Register a packaged server build from S3 as a GameLift build, create a new
fleet for it, wait until the fleet is ACTIVE, then flip the GameLift alias to the
new fleet. The previously-active fleet is retained so you can 'fabrica deploy
rollback' to it.

Requires 'fabrica deploy setup'. The build must already be uploaded to S3 (by
CI/Horde). By default the S3 location is deploy.buildBucket + "builds/<version>/
server.zip"; override with --s3-bucket / --s3-key.`,
		Example: `  # Preview the plan + monthly cost without creating anything:
  fabrica deploy promote v1.2.3 --dry-run

  # Roll out a build (waits for the fleet to go ACTIVE, then flips the alias):
  fabrica deploy promote v1.2.3

  # Point at a specific build artifact in S3:
  fabrica deploy promote v1.2.3 --s3-key builds/v1.2.3/LinuxServer.zip

  # Start fleet creation and return immediately (no alias flip — track with
  # 'fabrica deploy status', then re-run without --no-wait once ACTIVE):
  fabrica deploy promote v1.2.3 --no-wait

  # Prereqs: 'fabrica deploy setup' has run and the build zip is uploaded to
  # s3://<deploy.buildBucket>/builds/v1.2.3/server.zip (by CI/Horde).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:      rt,
				buildVersion: args[0],
				s3Bucket:     s3Bucket,
				s3Key:        s3Key,
				wait:         !noWait,
				dryRun:       opts.DryRun,
				assumeYes:    opts.AssumeYes,
				out:          out,
				costs:        fabricacost.Global,
				readState:    func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState:   fabricastate.WriteState,
				confirm:      prompt.Confirm,
				sleep:        time.Sleep,
				now:          time.Now,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.createResource = rc.Create
					c.updateResource = rc.Update
					c.getResource = rc.Get
				}
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.createFleetAsync = glm.CreateFleetAsync
					c.fleetStatus = glm.FleetStatus
					c.fleetEvents = glm.FleetEvents
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&s3Bucket, "s3-bucket", "", "S3 bucket holding the build (default: deploy.buildBucket)")
	cmd.Flags().StringVar(&s3Key, "s3-key", "", "S3 key of the build zip (default: builds/<version>/server.zip)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return after starting fleet creation without waiting for ACTIVE (skips alias flip)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("deploy is not set up. Run 'fabrica deploy setup' first")
	}
	role, ok := stateutil.ResourceByType(m, deploy.TypeAWSIAMRole)
	if !ok {
		return fmt.Errorf("deploy IAM role not found in state. Run 'fabrica deploy setup' first")
	}
	alias, ok := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)
	if !ok {
		return fmt.Errorf("deploy alias not found in state. Run 'fabrica deploy setup' first")
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", account, role.Identifier)
	plan := deploy.NewPromotePlan(c.runtime.Config.Deploy, account, region, c.buildVersion, roleARN, alias.Identifier, c.s3Bucket, c.s3Key)

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	c.printPlan(plan)
	if !c.assumeYes {
		if !c.confirm("Register this build and create a new fleet?") {
			fmt.Fprintln(c.out, "Promote cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
	}

	return c.apply(ctx, st, m, plan)
}

func (c command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, plan *deploy.PromotePlan) error {
	if c.createResource == nil || c.createFleetAsync == nil {
		return fmt.Errorf("cloud provider does not support GameLift deployment — only AWS is supported in V1")
	}

	// 1. Register build.
	buildState, err := deploy.BuildDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building build desired state: %w", err)
	}
	buildRes := &cloud.Resource{TypeName: deploy.TypeGameLiftBuild, DesiredState: buildState}
	if err := c.createResource(ctx, buildRes); err != nil {
		return fmt.Errorf("registering GameLift build from s3://%s/%s: %w — verify the build exists and the deploy role can read it", plan.S3Bucket, plan.S3Key, err)
	}
	fmt.Fprintf(c.out, "Registered build: %s (version %s)\n", buildRes.Identifier, plan.BuildVersion)
	c.recordResource(m, fabricastate.ModuleResource{
		TypeName:   deploy.TypeGameLiftBuild,
		Identifier: buildRes.Identifier,
		Properties: map[string]string{"buildVersion": plan.BuildVersion},
	})
	_ = c.writeState(st)

	// 2. Create fleet (non-blocking).
	fleetState, err := deploy.FleetDesiredState(plan, buildRes.Identifier)
	if err != nil {
		return fmt.Errorf("building fleet desired state: %w", err)
	}
	fleetRes := &cloud.Resource{TypeName: deploy.TypeGameLiftFleet, DesiredState: fleetState}
	if err := c.createFleetAsync(ctx, fleetRes); err != nil {
		return fmt.Errorf("creating fleet: %w", err)
	}
	fmt.Fprintf(c.out, "Creating fleet: %s\n", fleetRes.Identifier)
	c.recordResource(m, fabricastate.ModuleResource{
		TypeName:   deploy.TypeGameLiftFleet,
		Identifier: fleetRes.Identifier,
		Properties: map[string]string{"buildVersion": plan.BuildVersion, "role": "provisioning"},
	})
	st.UpsertModule(moduleName, plan.BuildVersion, "provisioning", m.Resources)
	_ = c.writeState(st)

	if !c.wait {
		fmt.Fprintf(c.out, "\nFleet creation started. Track it with: fabrica deploy status\n")
		fmt.Fprintln(c.out, "Alias was NOT flipped (--no-wait). Re-run without --no-wait, or flip manually once ACTIVE.")
		return nil
	}

	// 3. Poll activation.
	if err := c.pollUntilActive(ctx, fleetRes.Identifier, plan); err != nil {
		return err
	}

	// 4. Flip alias.
	patch, err := deploy.AliasFlipPatch(fleetRes.Identifier)
	if err != nil {
		return fmt.Errorf("building alias flip patch: %w", err)
	}
	aliasRes := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, Identifier: plan.AliasID, DesiredState: patch}
	if err := c.updateResource(ctx, aliasRes); err != nil {
		return fmt.Errorf("fleet %s is ACTIVE but flipping the alias failed: %w — the old fleet still serves traffic; re-run 'fabrica deploy promote %s' or flip the alias manually", fleetRes.Identifier, err, plan.BuildVersion)
	}
	fmt.Fprintf(c.out, "Alias %s now points to fleet %s.\n", plan.AliasID, fleetRes.Identifier)

	// 5. Record rollback target: demote previous active fleet, promote new one.
	c.swapActiveFleet(m, fleetRes.Identifier)
	st.UpsertModule(moduleName, plan.BuildVersion, "ready", m.Resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Promote complete — %s is live on fleet %s.\n", plan.BuildVersion, fleetRes.Identifier)
	fmt.Fprintln(c.out, "Roll back with: fabrica deploy rollback")
	return nil
}

// pollUntilActive polls fleet status until ACTIVE, printing phase transitions.
// On ERROR or timeout it surfaces recent fleet events and returns an error
// WITHOUT flipping the alias.
func (c command) pollUntilActive(ctx context.Context, fleetID string, plan *deploy.PromotePlan) error {
	deadline := c.now().Add(time.Duration(plan.ActivationTimeoutMinutes) * time.Minute)
	last := ""
	for {
		info, err := c.fleetStatus(ctx, fleetID)
		if err != nil {
			return fmt.Errorf("polling fleet %s: %w", fleetID, err)
		}
		if info.Status != last {
			fmt.Fprintf(c.out, "  fleet %s: %s\n", fleetID, info.Status)
			last = info.Status
		}
		switch info.Status {
		case "ACTIVE":
			return nil
		case "ERROR", "DELETING", "TERMINATED":
			c.printFleetEvents(ctx, fleetID)
			return fmt.Errorf("fleet %s entered status %s before becoming ACTIVE — see events above and 'fabrica deploy status'; the alias was not changed", fleetID, info.Status)
		}
		if c.now().After(deadline) {
			c.printFleetEvents(ctx, fleetID)
			return fmt.Errorf("timed out after %d minutes waiting for fleet %s to become ACTIVE (status %s) — check 'fabrica deploy status'; the alias was not changed", plan.ActivationTimeoutMinutes, fleetID, info.Status)
		}
		c.sleep(pollInterval)
	}
}

func (c command) printFleetEvents(ctx context.Context, fleetID string) {
	if c.fleetEvents == nil {
		return
	}
	evs, err := c.fleetEvents(ctx, fleetID)
	if err != nil || len(evs) == 0 {
		return
	}
	fmt.Fprintln(c.out, "Recent fleet events:")
	for _, e := range evs {
		fmt.Fprintf(c.out, "  [%s] %s %s\n", e.Time, e.Code, e.Message)
	}
}

// recordResource adds or replaces a resource of the same type+identifier in the
// module's resource list.
func (c command) recordResource(m *fabricastate.ModuleState, r fabricastate.ModuleResource) {
	for i := range m.Resources {
		if m.Resources[i].TypeName == r.TypeName && m.Resources[i].Identifier == r.Identifier {
			m.Resources[i] = r
			return
		}
	}
	m.Resources = append(m.Resources, r)
}

// swapActiveFleet marks newFleetID active and demotes any other active fleet to
// superseded (the rollback candidate).
func (c command) swapActiveFleet(m *fabricastate.ModuleState, newFleetID string) {
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		if r.Properties == nil {
			r.Properties = map[string]string{}
		}
		switch {
		case r.Identifier == newFleetID:
			r.Properties["role"] = "active"
		case r.Properties["role"] == "active":
			r.Properties["role"] = "superseded"
		}
	}
}

func (c command) printDryRun(plan *deploy.PromotePlan) {
	fmt.Fprintln(c.out, "Deploy promote (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanDetails(plan)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to register the build and create the fleet.")
}

func (c command) printPlan(plan *deploy.PromotePlan) {
	fmt.Fprintln(c.out, "Promoting build to a new fleet...")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
	fmt.Fprintln(c.out, "The previously-active fleet is retained for rollback.")
	fmt.Fprintln(c.out)
}

func (c command) printPlanDetails(plan *deploy.PromotePlan) {
	fmt.Fprintf(c.out, "  Build version: %s\n", plan.BuildVersion)
	fmt.Fprintf(c.out, "  Build source:  s3://%s/%s\n", plan.S3Bucket, plan.S3Key)
	fmt.Fprintf(c.out, "  Fleet:         %s\n", plan.FleetName)
	fmt.Fprintf(c.out, "  Instance type: %s (%s)\n", plan.InstanceType, plan.FleetType)
	fmt.Fprintf(c.out, "  Launch path:   %s\n", plan.LaunchPath)
	fmt.Fprintf(c.out, "  Alias:         %s\n", plan.AliasID)
	fmt.Fprintln(c.out)
}
