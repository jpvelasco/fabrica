// Package approve provides the "horde agents approve" subcommand, which approves
// pending Horde agent enrollments through the coordinator's own enrollment API.
package approve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/prompt"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// remoteTimeout bounds each SSM call (the list step and the approval step).
const remoteTimeout = 90 * time.Second

type command struct {
	runtime   globals.Runtime
	hosts     []string
	all       bool
	dryRun    bool
	assumeYes bool
	jsonOut   bool
	out       io.Writer
	readState func(context.Context) (*state.State, error)
	runRemote func(context.Context, string, []string) (cloud.RemoteResult, error)
	confirm   func(string) bool
}

// result is the report shared by text and --json output.
type result struct {
	DryRun   bool     `json:"dryRun"`
	Pending  []string `json:"pending"`
	Selected []string `json:"selected"`
	Approved []string `json:"approved"`
	Unnamed  int      `json:"unnamed"`
}

// New returns the "horde agents approve" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var hosts []string
	var all bool
	cmd := &cobra.Command{
		Use:   "approve",
		Short: "Approve pending Horde agent enrollments",
		Long: `List or approve Horde build agents waiting for enrollment.

With no selector, lists the pending enrollments on the recorded coordinator
and exits. Approve by exact hostName with --host (repeatable) or approve every
pending enrollment with --all. Approval is all-or-nothing: if any requested
host is not pending, or is pending more than once, nothing is approved.

The Admin password and enrollment keys stay on the coordinator. The command
runs its script over SSM and prints only hostnames and counts. It does not
lock Fabrica state, because it changes Horde state, not Fabrica's.

Use --dry-run to list and preview the selection without approving. Use --yes
to skip the confirmation prompt.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{runtime: rt, hosts: hosts, all: all, dryRun: opts.DryRun, assumeYes: opts.AssumeYes,
				jsonOut: opts.JSONOutput, out: out, confirm: prompt.Confirm}
			c.readState = func(ctx context.Context) (*state.State, error) { return provision.ReadState(ctx, rt) }
			if rt.Provider != nil {
				if remote, ok := rt.Provider.(cloud.RemoteRunner); ok {
					c.runRemote = remote.RunCommand
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringArrayVar(&hosts, "host", nil, "Approve this pending agent hostName exactly (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "Approve every pending agent that has a valid hostName")
	cmd.MarkFlagsMutuallyExclusive("host", "all")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if err := horde.ValidateEnrollmentHosts(c.hosts); err != nil {
		return err
	}
	st, err := c.readState(ctx)
	if err != nil {
		return err
	}
	m := st.GetModule("horde")
	if m == nil {
		return fmt.Errorf("Horde coordinator is not recorded; run 'fabrica horde create' first")
	}
	conn, err := horde.ResolveConnection(c.runtime.Config.Horde, m)
	if err != nil {
		return err
	}
	inst, ok := horde.CoordinatorResource(m, cloud.TypeAWSEC2Instance)
	if !ok || inst.Identifier == "" {
		return fmt.Errorf("Horde coordinator instance is not recorded; re-run 'fabrica horde create' to restore it")
	}
	if c.runRemote == nil {
		return fmt.Errorf("horde agents approve needs SSM Run Command; use the AWS provider with SSM access")
	}
	c.warnIfNoPool(m)
	listScript, err := horde.EnrollmentScript(conn, horde.EnrollmentList, nil)
	if err != nil {
		return err
	}
	pending, err := c.remote(ctx, inst.Identifier, listScript, "list")
	if err != nil {
		return err
	}
	if len(c.hosts) == 0 && !c.all {
		return c.emit(result{Pending: pending.Pending, Unnamed: pending.Unnamed})
	}
	selected, err := c.selection(pending)
	if err != nil {
		return err
	}
	base := result{Pending: pending.Pending, Selected: selected, Unnamed: pending.Unnamed}
	if c.dryRun {
		base.DryRun = true
		return c.emit(base)
	}
	if len(selected) == 0 {
		return c.emit(base)
	}
	if !c.assumeYes && !c.confirm(fmt.Sprintf("Approve %d Horde agent enrollment(s): %s?", len(selected), strings.Join(selected, ", "))) {
		fmt.Fprintln(c.out, "Approval cancelled. No agents were approved.")
		return nil
	}
	approveScript, err := horde.EnrollmentScript(conn, horde.EnrollmentApprove, selected)
	if err != nil {
		return err
	}
	approved, err := c.remote(ctx, inst.Identifier, approveScript, "approval")
	if err != nil {
		return err
	}
	base.Approved = approved.Approved
	return c.emit(base)
}

// selection resolves --host or --all against the pending list. Every check
// happens here, before any approval call is sent.
func (c command) selection(pending horde.EnrollmentReport) ([]string, error) {
	counts := make(map[string]int, len(pending.Pending))
	for _, name := range pending.Pending {
		counts[name]++
	}
	if c.all {
		if pending.Unnamed > 0 {
			return nil, fmt.Errorf("%d pending enrollment(s) have no valid hostName and cannot be approved by name; resolve them in the Horde UI, then retry", pending.Unnamed)
		}
		for _, name := range pending.Pending {
			if counts[name] > 1 {
				return nil, duplicateError(name, counts[name])
			}
		}
		return pending.Pending, nil
	}
	for _, host := range c.hosts {
		switch n := counts[host]; {
		case n == 0:
			return nil, fmt.Errorf("host %q is not pending; run 'fabrica horde agents approve' with no selector to list pending agents", host)
		case n > 1:
			return nil, duplicateError(host, n)
		}
	}
	return c.hosts, nil
}

func duplicateError(host string, n int) error {
	return fmt.Errorf("host %q has %d pending enrollments; resolve the duplicates in the Horde UI, then retry", host, n)
}

func (c command) remote(ctx context.Context, instanceID, script, step string) (horde.EnrollmentReport, error) {
	runCtx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	res, err := c.runRemote(runCtx, instanceID, []string{script})
	if err != nil || res.ExitCode != 0 {
		if runCtx.Err() != nil {
			return horde.EnrollmentReport{}, fmt.Errorf("Horde agent enrollment %s interrupted: %w; re-run 'fabrica horde agents approve' to see what is still pending", step, runCtx.Err())
		}
		return horde.EnrollmentReport{}, fmt.Errorf("Horde agent enrollment %s failed on the coordinator; confirm the coordinator is running and its SSM agent is online, then re-run 'fabrica horde agents approve'", step)
	}
	return horde.ParseEnrollmentOutput(res.Stdout)
}

// warnIfNoPool notes, in text mode only, that enrollments are handled even
// though Fabrica has no agent pool recorded. Enrollments are Horde state, so
// this is informational, not a failure.
func (c command) warnIfNoPool(m *state.ModuleState) {
	for _, r := range m.Resources {
		if r.Properties["role"] == "agent" {
			return
		}
	}
	if !c.jsonOut {
		fmt.Fprintln(c.out, "Note: no agent pool is recorded in Fabrica state; enrollments are listed from the coordinator anyway.")
	}
}

func (c command) emit(r result) error {
	if r.Pending == nil {
		r.Pending = []string{}
	}
	if r.Selected == nil {
		r.Selected = []string{}
	}
	if r.Approved == nil {
		r.Approved = []string{}
	}
	if c.jsonOut {
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	c.printText(r)
	return nil
}

func (c command) printText(r result) {
	switch {
	case len(r.Approved) > 0:
		fmt.Fprintf(c.out, "Approved %d agent enrollment(s): %s\n", len(r.Approved), strings.Join(r.Approved, ", "))
	case r.DryRun:
		c.printPending(r)
		if len(r.Selected) == 0 {
			fmt.Fprintln(c.out, "Dry run: nothing to approve.")
			return
		}
		fmt.Fprintf(c.out, "Dry run: would approve %s. No approval was sent.\n", strings.Join(r.Selected, ", "))
	default:
		c.printPending(r)
	}
}

func (c command) printPending(r result) {
	if len(r.Pending) == 0 {
		fmt.Fprintln(c.out, "No pending agent enrollments.")
	} else {
		fmt.Fprintln(c.out, "Pending agent enrollments:")
		for _, name := range r.Pending {
			fmt.Fprintf(c.out, "  %s\n", name)
		}
		fmt.Fprintln(c.out, "Approve with: fabrica horde agents approve --host <hostName>, or --all")
	}
	if r.Unnamed > 0 {
		fmt.Fprintf(c.out, "%d pending enrollment(s) have no valid hostName; resolve them in the Horde UI.\n", r.Unnamed)
	}
}
