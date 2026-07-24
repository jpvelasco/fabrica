// Package destroy implements "fabrica ci destroy": tear down the CI
// infrastructure — the CodeBuild project (via the CodeBuildRunner SDK, since
// AWS::CodeBuild::Project has no Cloud Control CREATE/DELETE) and the IAM role
// (via Cloud Control). State is persisted after each deletion so a partial
// failure is recoverable.
package destroy

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const moduleName = "ci"

type command struct {
	runtime     globals.Runtime
	dryRun      bool
	assumeYes   bool
	skipConfirm bool
	jsonOut     bool
	out         io.Writer
}

// New returns the "ci destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Tear down the CI infrastructure (CodeBuild project + IAM role)",
		Long: `Delete the Fabrica CI resources: the CodeBuild project and its IAM service role.

Deletion order is project, then role. A missing project is not an error. You are
asked to type a confirmation phrase before any deletion; pass --yes to skip, or
--dry-run to preview.`,
		Example: `  fabrica ci destroy --dry-run
  fabrica ci destroy
  fabrica ci destroy --yes`,
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
				jsonOut:   opts.JSONOutput,
				out:       out,
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	tc := c.buildTeardown()
	return tc.Run(ctx)
}

func (c command) buildTeardown() teardown.Command {
	var deleteProjectFn func(ctx context.Context, name string) error
	if c.runtime.Provider != nil {
		if r, ok := c.runtime.Provider.(cloud.CodeBuildRunner); ok {
			deleteProjectFn = r.DeleteProject
		}
	}
	tc := teardown.Command{
		Spec: teardown.Spec{
			ModuleName:     moduleName,
			Verb:           "destroy",
			VersionLabel:   "Project",
			Title:          "CI",
			NotProvisioned: "CI is not provisioned. Nothing to destroy.",
			PlanHeader:     "CI — destroy plan",
			DryRunHeader:   "CI (destroy dry run)",
			Irreversible:   "IRREVERSIBLE: deletes the CodeBuild project and IAM role.",
			SuccessMessage: "CI infrastructure destroyed.",
			ResourceOrder:  ciResourceOrder,
		},
		Runtime:     c.runtime,
		DryRun:      c.dryRun,
		AssumeYes:   c.assumeYes,
		JSONOut:     c.jsonOut,
		Out:         c.out,
		SkipConfirm: c.skipConfirm,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(c.runtime) },
		WriteState:  fabricastate.WriteState,
		SDKDeleteFunc: func(ctx context.Context, typeName, identifier string) error {
			if typeName == ci.TypeAWSCodeBuildProject {
				if deleteProjectFn == nil {
					return fmt.Errorf("cloud provider does not support CodeBuild project deletion — only AWS is supported in V1")
				}
				return deleteProjectFn(ctx, identifier)
			}
			// Not a CodeBuild resource — fall back to Cloud Control for IAM role.
			return cloud.ErrNotHandled
		},
	}
	teardown.WireProvider(&tc, c.runtime)
	return tc
}

// ciResourceOrder returns the deletion-ordered resources for the CI module:
// CodeBuild project first, then IAM role.
func ciResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	var project, role *fabricastate.ModuleResource
	for i := range m.Resources {
		switch m.Resources[i].TypeName {
		case ci.TypeAWSCodeBuildProject:
			project = &m.Resources[i]
		case ci.TypeAWSIAMRole:
			role = &m.Resources[i]
		}
	}
	var out []cloud.Resource
	if project != nil {
		out = append(out, cloud.Resource{TypeName: project.TypeName, Identifier: project.Identifier})
	}
	if role != nil {
		out = append(out, cloud.Resource{TypeName: role.TypeName, Identifier: role.Identifier})
	}
	return out
}

// RunOrchestrated runs the CI teardown with confirmation skipped, for use by
// `fabrica destroy --all`. The aggregate confirmation is handled by the orchestrator.
func RunOrchestrated(ctx context.Context, rt globals.Runtime, out io.Writer) error {
	c := command{runtime: rt, skipConfirm: true, assumeYes: true, out: out}
	tc := c.buildTeardown()
	return tc.Run(ctx)
}
