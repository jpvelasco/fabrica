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
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
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
	runtime     globals.Runtime
	dryRun      bool
	assumeYes   bool
	skipConfirm bool
	jsonOut     bool
	out         io.Writer

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	deleteProject  func(ctx context.Context, name string) error
	deleteResource func(ctx context.Context, r *cloud.Resource) error
	confirm        func(string, string) bool
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
				runtime:    rt,
				dryRun:     opts.DryRun,
				assumeYes:  opts.AssumeYes,
				jsonOut:    opts.JSONOutput,
				out:        out,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.ConfirmExact,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.deleteResource = rc.Delete
				}
				if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.deleteProject = r.DeleteProject
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		fmt.Fprintln(c.out, "CI is not provisioned. Nothing to destroy.")
		return nil
	}

	project, hasProject := stateutil.ResourceByType(m, ci.TypeAWSCodeBuildProject)
	role, hasRole := stateutil.ResourceByType(m, ci.TypeAWSIAMRole)

	if c.dryRun {
		fmt.Fprintln(c.out, "CI (destroy dry run)")
		fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
		fmt.Fprintln(c.out, "Resources that would be deleted (in order):")
		if hasProject {
			fmt.Fprintf(c.out, "  1. %s: %s\n", ci.TypeAWSCodeBuildProject, project.Identifier)
		}
		if hasRole {
			fmt.Fprintf(c.out, "  2. %s: %s\n", ci.TypeAWSIAMRole, role.Identifier)
		}
		fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
		return nil
	}

	account := st.Account
	if c.runtime.Config != nil && c.runtime.Config.Cloud.AWS.AccountID != "" {
		account = c.runtime.Config.Cloud.AWS.AccountID
	}

	if !c.skipConfirm {
		phrase := fmt.Sprintf("destroy %s %s", moduleName, account)
		if !c.assumeYes {
			fmt.Fprintln(c.out, "CI — destroy plan")
			fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
			if hasProject {
				fmt.Fprintf(c.out, "  CodeBuild project: %s\n", project.Identifier)
			}
			if hasRole {
				fmt.Fprintf(c.out, "  IAM role:          %s\n", role.Identifier)
			}
			fmt.Fprintln(c.out, "IRREVERSIBLE: deletes the CodeBuild project and IAM role.")
			fmt.Fprintln(c.out)
			fmt.Fprintf(c.out, "Type this exact phrase to continue:\n\n  %s\n\n", phrase)
			if !c.confirm("Enter confirmation phrase", phrase) {
				fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
				return nil
			}
			fmt.Fprintln(c.out, "Confirmation accepted.")
		} else {
			fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
		}
	}

	return c.apply(ctx, st, m, project, hasProject, role, hasRole)
}

func (c command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, project fabricastate.ModuleResource, hasProject bool, role fabricastate.ModuleResource, hasRole bool) error {
	if hasProject {
		if c.deleteProject == nil {
			return fmt.Errorf("cloud provider does not support CodeBuild project deletion — only AWS is supported in V1")
		}
		fmt.Fprintf(c.out, "Deleting CodeBuild project %s...\n", project.Identifier)
		if err := c.deleteProject(ctx, project.Identifier); err != nil {
			return fmt.Errorf("deleting CodeBuild project %s: %w", project.Identifier, err)
		}
		fmt.Fprintf(c.out, "  Deleted: %s\n", project.Identifier)
		c.removeAndPersist(st, m, ci.TypeAWSCodeBuildProject)
	}

	if hasRole {
		if c.deleteResource == nil {
			return fmt.Errorf("no provider configured; run 'fabrica setup' first")
		}
		fmt.Fprintf(c.out, "Deleting IAM role %s...\n", role.Identifier)
		r := &cloud.Resource{TypeName: ci.TypeAWSIAMRole, Identifier: role.Identifier}
		if err := c.deleteResource(ctx, r); err != nil {
			return fmt.Errorf("deleting IAM role %s: %w", role.Identifier, err)
		}
		fmt.Fprintf(c.out, "  Deleted: %s\n", role.Identifier)
		c.removeAndPersist(st, m, ci.TypeAWSIAMRole)
	}

	removeModule(st, moduleName)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
	fmt.Fprintln(c.out, "CI infrastructure destroyed.")
	return nil
}

func (c command) removeAndPersist(st *fabricastate.State, m *fabricastate.ModuleState, typeName string) {
	filtered := m.Resources[:0]
	for _, r := range m.Resources {
		if r.TypeName != typeName {
			filtered = append(filtered, r)
		}
	}
	m.Resources = filtered
	st.UpsertModule(moduleName, m.Version, "destroying", m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
}

func removeModule(st *fabricastate.State, name string) {
	filtered := st.Modules[:0]
	for _, m := range st.Modules {
		if m.Name != name {
			filtered = append(filtered, m)
		}
	}
	st.Modules = filtered
}
