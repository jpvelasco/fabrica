// Package destroy implements "fabrica deploy destroy": tear down deploy
// resources. By default it deletes only the fleets and builds, preserving the
// long-lived alias and IAM role (game backends reference the alias). Pass --all
// to also remove the alias and role.
package destroy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const moduleName = "deploy"

// resourceOrder returns the teardown ordering hook for the given --all setting.
// Fleets are deleted first, then builds (a fleet references its build); with
// all=true the alias then the role follow (deleted last).
func resourceOrder(all bool) func(*fabricastate.ModuleState) []cloud.Resource {
	return func(m *fabricastate.ModuleState) []cloud.Resource {
		var fleets, builds, alias, role []cloud.Resource
		for _, r := range m.Resources {
			res := cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier}
			switch r.TypeName {
			case deploy.TypeGameLiftFleet:
				fleets = append(fleets, res)
			case deploy.TypeGameLiftBuild:
				builds = append(builds, res)
			case deploy.TypeGameLiftAlias:
				alias = append(alias, res)
			case deploy.TypeAWSIAMRole:
				role = append(role, res)
			}
		}
		fleets = append(fleets, builds...)
		if all {
			fleets = append(fleets, alias...)
			fleets = append(fleets, role...)
		}
		return fleets
	}
}

func spec(all bool) teardown.Spec {
	s := teardown.Spec{
		ModuleName:     moduleName,
		Verb:           "destroy",
		VersionLabel:   "Version",
		Title:          "GameLift deployment",
		NotProvisioned: "Deploy is not provisioned. Nothing to destroy.",
		PlanHeader:     "GameLift deployment — destroy plan",
		DryRunHeader:   "GameLift deployment (destroy dry run)",
		SuccessMessage: "GameLift deployment resources destroyed.",
		ResourceOrder:  resourceOrder(all),
	}
	if all {
		s.Irreversible = "IRREVERSIBLE: deletes all fleets, builds, the alias, and the IAM role."
	} else {
		s.Irreversible = "IRREVERSIBLE: deletes all fleets and builds. The alias and IAM role are PRESERVED (use --all to remove them)."
	}
	return s
}

// newForTest builds the teardown.Command without provider wiring, for unit tests.
func newForTest(all bool) teardown.Command {
	return teardown.Command{Spec: spec(all)}
}

// NewTeardown builds the deploy teardown.Command for orchestrated use by
// `fabrica destroy --all`, with all=true so the alias and IAM role are removed
// (a full-stack wipe leaves nothing behind).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	tc := teardown.Command{
		Spec:        spec(true),
		Runtime:     rt,
		SkipConfirm: true,
		AssumeYes:   true,
		Out:         out,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		WriteState:  fabricastate.WriteState,
	}
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = rc.Delete
			tc.GetResource = rc.Get
		}
	}
	return tc
}

// New returns the "deploy destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down deploy resources (fleets + builds; --all also removes alias + role)",
		Long: `Tear down GameLift deploy resources.

By default this deletes the fleets and builds but PRESERVES the GameLift alias
and IAM role, because game clients/backends reference the alias and it is meant
to outlive individual deployments. Pass --all to remove the alias and role too
(symmetric with 'fabrica deploy setup').

Active game sessions are not drained automatically; if a fleet refuses to delete,
GameLift's error explains why — terminate sessions or wait, then retry.`,
		Example: `  fabrica deploy destroy            # fleets + builds only (alias/role kept)
  fabrica deploy destroy --all      # everything, incl. alias + role
  fabrica deploy destroy --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			tc := teardown.Command{
				Spec:       spec(all),
				Runtime:    rt,
				DryRun:     opts.DryRun,
				AssumeYes:  opts.AssumeYes,
				JSONOut:    opts.JSONOutput,
				Out:        out,
				Confirm:    prompt.ConfirmExact,
				ReadState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				WriteState: fabricastate.WriteState,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					tc.DeleteResource = rc.Delete
					tc.GetResource = rc.Get
				}
			}
			if !opts.JSONOutput && !all && !opts.DryRun {
				cmd.Printf("Note: the GameLift alias and IAM role will be preserved. Use --all to remove them.\n\n")
			}
			return tc.Run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Also delete the GameLift alias and IAM role")
	return cmd
}
