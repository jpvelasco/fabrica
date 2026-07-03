package destroy

import (
	"context"
	"fmt"
	"io"

	cidestroy "github.com/jpvelasco/fabrica/cmd/ci/destroy"
	deploydestroy "github.com/jpvelasco/fabrica/cmd/deploy/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	hordedestroy "github.com/jpvelasco/fabrica/cmd/horde/destroy"
	destroyall "github.com/jpvelasco/fabrica/cmd/internal/destroyall"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	pfdestroy "github.com/jpvelasco/fabrica/cmd/perforce/destroy"
	wsterminate "github.com/jpvelasco/fabrica/cmd/workstation/terminate"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type command struct {
	runtime   globals.Runtime
	all       bool
	dryRun    bool
	assumeYes bool
	out       io.Writer
	confirm   func(string, string) bool
	runAll    func(context.Context) error
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down provisioned infrastructure",
		Long: `Safely dismantle Fabrica-managed infrastructure.

By default, this command shows a summary of what would be destroyed
if --all is provided. It never mutates infrastructure without explicit
confirmation.

Use --all to target all provisioned resources. The command will walk
through a confirmation dialog before proceeding.

Run with --all --yes to skip the interactive prompt (use with care).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				all:       all,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				out:       out,
				confirm:   prompt.ConfirmExact,
			}
			c.runAll = func(ctx context.Context) error {
				return runAll(ctx, rt, opts, out)
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include all provisioned infrastructure")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if !c.all {
		c.printUsageHint()
		return nil
	}
	if c.runAll != nil {
		return c.runAll(ctx)
	}
	return nil
}

func runAll(ctx context.Context, rt globals.Runtime, opts globals.Options, out io.Writer) error {
	if rt.Provider == nil {
		fmt.Fprintln(out, "No infrastructure found. Nothing to destroy.")
		return nil
	}
	account, _, region, err := rt.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}
	st, err := provision.ReadState(rt)
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	// Ordered candidate modules: deploy, ci, workstation, horde, perforce.
	// Include a module only if it is present in state.
	var mods []destroyall.Module
	add := func(name string, td destroyall.ModuleTeardown) {
		if st.GetModule(name) != nil {
			mods = append(mods, destroyall.Module{Name: name, Teardown: td})
		}
	}
	add("deploy", teardownClosure(ctx, deploydestroy.NewTeardown(rt, out)))
	add("ci", ciTeardownClosure(ctx, rt, out))
	add("workstation", teardownClosure(ctx, wsterminate.NewTeardown(rt, out)))
	add("horde", teardownClosure(ctx, hordedestroy.NewTeardown(rt, out)))
	add("perforce", teardownClosure(ctx, pfdestroy.NewTeardown(rt, out)))

	names := fabricastate.ResolveBackendNames(rt.Config, account)
	destroyer, _ := rt.Provider.(cloud.StateBackendDestroyer)

	e := destroyall.Engine{
		Account:   account,
		Region:    region,
		Bucket:    names.Bucket,
		Table:     names.Table,
		Modules:   mods,
		Backend:   destroyer,
		DryRun:    opts.DryRun,
		AssumeYes: opts.AssumeYes,
		JSONOut:   opts.JSONOutput,
		Out:       out,
		Confirm:   prompt.ConfirmExact,
	}
	return e.Run(ctx)
}

// teardownClosure adapts a teardown.Command into a destroyall.ModuleTeardown.
// teardown.Command.Run returns only an error and prints deleted IDs to Out, so
// the returned ID slice is nil (the per-module output already lists them).
func teardownClosure(_ context.Context, tc teardown.Command) destroyall.ModuleTeardown {
	return func(ctx context.Context) ([]string, error) {
		return nil, tc.Run(ctx)
	}
}

// ciTeardownClosure builds the CI teardown closure. CI is not a teardown.Command
// (its CodeBuild project is SDK-managed); it uses cmd/ci/destroy's orchestrated path.
func ciTeardownClosure(_ context.Context, rt globals.Runtime, out io.Writer) destroyall.ModuleTeardown {
	return func(ctx context.Context) ([]string, error) {
		return nil, cidestroy.RunOrchestrated(ctx, rt, out)
	}
}

func (c command) printUsageHint() {
	fmt.Fprintln(c.out, "To destroy infrastructure, use --all:")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "  fabrica destroy --all")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "This requires explicit confirmation. Use --all --yes to skip the prompt.")
}
