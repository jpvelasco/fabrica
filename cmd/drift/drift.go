// Package driftcmd implements `fabrica drift`: drift detection and optional
// auto-remediation. The default mode is read-only; `--fix` enables recreating
// Missing managed resources from recorded state.
package driftcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/drift"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const lineWidth = 64

type command struct {
	runtime        globals.Runtime
	jsonOut        bool
	fixMode        bool
	assumeYes      bool
	out            io.Writer
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
	listResources  func(ctx context.Context, typeName string) ([]cloud.Resource, error)
	createResource func(ctx context.Context, r *cloud.Resource) error
	backend        cloud.StateBackendChecker
	codebuild      cloud.CodeBuildRunner
	confirm        func(string) bool
}

// New returns the "fabrica drift" command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Detect drift between recorded state and live AWS resources",
		Long: `Compare recorded state (.fabrica/state.json) against live AWS resources
and report whether each resource is in sync, missing, or has attribute
mismatches.

This command is read-only by default. Use --fix to auto-remediate Missing
managed resources (EC2 instances and security groups) by recreating them
from recorded state. Mismatch and Extra resources are report-only.

Checks the state backend (S3 bucket, DynamoDB table), EC2 instances
(existence, state, instance type, AMI), security groups, IAM roles,
and CodeBuild projects.`,
		Example: `  # Check drift for all provisioned modules:
  fabrica drift

  # Preview what --fix would do:
  fabrica drift --fix --dry-run

  # Auto-fix missing resources:
  fabrica drift --fix

  # Machine-readable output:
  fabrica drift --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			fixMode, _ := cmd.Flags().GetBool("fix")

			c := command{
				runtime:    rt,
				jsonOut:    opts.JSONOutput,
				fixMode:    fixMode,
				assumeYes:  opts.AssumeYes,
				out:        out,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.Confirm,
			}
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
				c.listResources = rt.Provider.Resources().List
				c.createResource = rt.Provider.Resources().Create
				if b, ok := rt.Provider.(cloud.StateBackendChecker); ok {
					c.backend = b
				}
				if cb, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.codebuild = cb
				}
			}
			return c.run(cmd.Context(), opts.DryRun)
		},
	}
	cmd.Flags().BoolP("fix", "f", false, "Auto-remediate Missing managed resources (EC2 instances and security groups)")
	return cmd
}

func (c *command) run(ctx context.Context, dryRun bool) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	engine := &drift.Engine{
		State:           st,
		ResourceGet:     c.getResource,
		ResourceList:    c.listResources,
		BackendChecker:  c.backend,
		CodeBuildRunner: c.codebuild,
		Config: &drift.DriftConfig{
			Account: c.runtime.Config.Cloud.AWS.AccountID,
			Region:  c.runtime.Config.Cloud.AWS.Region,
			Bucket:  c.runtime.Config.State.Bucket,
			Table:   c.runtime.Config.State.Table,
		},
	}

	report := engine.Run(ctx)

	// Fix mode: plan remediation and optionally apply.
	if c.fixMode {
		return c.runFix(ctx, dryRun, st, report)
	}

	// Default: read-only report.
	if c.jsonOut {
		return c.printJSON(report)
	}
	c.printText(report)
	return nil
}

func (c *command) runFix(ctx context.Context, dryRun bool, st *fabricastate.State, report *drift.DriftReport) error {
	plan := drift.PlanRemediation(report, st, c.runtime.Config)

	if len(plan.Actions) == 0 {
		if c.jsonOut {
			return c.printFixJSON(nil, plan, nil)
		}
		fmt.Fprintln(c.out, "No drift found — nothing to fix.")
		return nil
	}

	if dryRun {
		if c.jsonOut {
			return c.printFixJSON(nil, plan, nil)
		}
		c.printFixPlan(plan)
		return nil
	}

	// Real fix: require confirmation unless --yes.
	if !c.assumeYes && c.confirm != nil {
		msg := fmt.Sprintf("Fix %d missing resource(s) — %d will be recreated, %d report-only", plan.ToFix, plan.ToFix, plan.ToSkip)
		if !c.confirm(msg) {
			fmt.Fprintln(c.out, "Aborted — no changes made.")
			return nil
		}
	}

	// Apply remediation.
	result := drift.ApplyRemediation(ctx, plan, st, c.createResource)

	// Write updated state if anything was applied.
	if len(result.Applied) > 0 && c.writeState != nil {
		if err := c.writeState(st); err != nil {
			return fmt.Errorf("writing state after fix: %w", err)
		}
	}

	if c.jsonOut {
		return c.printFixJSON(result, plan, nil)
	}
	c.printFixResult(result, plan)
	if len(result.Failed) > 0 {
		return fmt.Errorf("remediation failed: %d action(s) failed", len(result.Failed))
	}
	return nil
}

func (c *command) printJSON(report *drift.DriftReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding drift report: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c *command) printText(report *drift.DriftReport) {
	fmt.Fprintln(c.out, "Fabrica drift detection")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintln(c.out)

	c.printBackend(report.Backend)
	fmt.Fprintln(c.out)

	if len(report.Modules) == 0 {
		fmt.Fprintln(c.out, "No modules provisioned — nothing to check.")
		return
	}

	for i := range report.Modules {
		md := &report.Modules[i]
		c.printModule(md)
		if i < len(report.Modules)-1 {
			fmt.Fprintln(c.out)
		}
	}

	fmt.Fprintln(c.out)
	c.printSummary(report)
}

func (c *command) printBackend(b drift.DriftBackend) {
	fmt.Fprintln(c.out, "State Backend")
	fmt.Fprintln(c.out, strings.Repeat("-", 40))
	if b.Bucket != "" {
		fmt.Fprintf(c.out, "  %s Bucket:  %s\n", statusSymbol(b.BucketStatus), b.Bucket)
		if b.BucketDetails != "" {
			fmt.Fprintf(c.out, "        %s\n", b.BucketDetails)
		}
	}
	if b.Table != "" {
		fmt.Fprintf(c.out, "  %s Table:   %s\n", statusSymbol(b.TableStatus), b.Table)
		if b.TableDetails != "" {
			fmt.Fprintf(c.out, "        %s\n", b.TableDetails)
		}
	}
}

func (c *command) printModule(md *drift.ModuleDrift) {
	fmt.Fprintf(c.out, "  Module: %s\n", md.Name)
	fmt.Fprintln(c.out, strings.Repeat("-", 40))
	for i := range md.Resources {
		r := &md.Resources[i]
		fmt.Fprintf(c.out, "  %s %-28s %s\n", statusSymbol(r.Status), r.TypeName, r.Identifier)
		if r.Details != "" {
			fmt.Fprintf(c.out, "        %s\n", r.Details)
		}
	}
}

func (c *command) printSummary(report *drift.DriftReport) {
	fmt.Fprintln(c.out, "Summary")
	fmt.Fprintln(c.out, strings.Repeat("-", 40))
	fmt.Fprintf(c.out, "  Checked:  %d\n", report.Checked)
	fmt.Fprintf(c.out, "  In sync:  %d\n", report.InSync)
	if report.Missing > 0 {
		fmt.Fprintf(c.out, "  Missing:  %d\n", report.Missing)
	}
	if report.Extra > 0 {
		fmt.Fprintf(c.out, "  Extra:    %d\n", report.Extra)
	}
	if report.Mismatch > 0 {
		fmt.Fprintf(c.out, "  Mismatch: %d\n", report.Mismatch)
	}
	if report.Errors > 0 {
		fmt.Fprintf(c.out, "  Errors:   %d\n", report.Errors)
	}
}

func statusSymbol(s drift.DriftStatus) string {
	switch s {
	case drift.InSync:
		return "[OK]  "
	case drift.Missing:
		return "[FAIL]"
	case drift.Extra:
		return "[WARN]"
	case drift.Mismatch:
		return "[WARN]"
	case drift.Error:
		return "[????]"
	default:
		return "[????]"
	}
}

// --- Fix mode output ---

func (c *command) printFixPlan(plan *drift.RemediationPlan) {
	fmt.Fprintln(c.out, "Drift remediation plan (--dry-run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintln(c.out)

	for i := range plan.Actions {
		a := &plan.Actions[i]
		switch a.Kind {
		case drift.ActionCreate:
			fmt.Fprintf(c.out, "  [FIX]  %s/%s %s — will recreate from recorded state\n",
				a.Module, a.TypeName, a.Identifier)
		case drift.ActionSkip:
			fmt.Fprintf(c.out, "  [SKIP] %s/%s %s — %s\n",
				a.Module, a.TypeName, a.Identifier, a.Reason)
		}
	}

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  To fix:   %d\n", plan.ToFix)
	fmt.Fprintf(c.out, "  To skip:  %d (report-only)\n", plan.ToSkip)
}

func (c *command) printFixResult(result *drift.RemediationResult, plan *drift.RemediationPlan) {
	fmt.Fprintln(c.out, "Drift remediation result")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintln(c.out)

	if len(result.Applied) > 0 {
		fmt.Fprintln(c.out, "  Applied:")
		for i := range result.Applied {
			a := &result.Applied[i]
			fmt.Fprintf(c.out, "    [OK]  %s/%s %s\n", a.Module, a.TypeName, a.Identifier)
		}
		fmt.Fprintln(c.out)
	}

	if len(result.Skipped) > 0 {
		fmt.Fprintln(c.out, "  Skipped (report-only):")
		for i := range result.Skipped {
			a := &result.Skipped[i]
			fmt.Fprintf(c.out, "    [SKIP] %s/%s %s — %s\n",
				a.Module, a.TypeName, a.Identifier, a.Reason)
		}
		fmt.Fprintln(c.out)
	}

	if len(result.Failed) > 0 {
		fmt.Fprintln(c.out, "  Failed:")
		for i := range result.Failed {
			a := &result.Failed[i]
			fmt.Fprintf(c.out, "    [FAIL] %s/%s %s\n", a.Module, a.TypeName, a.Identifier)
		}
		for i := range result.Errors {
			fmt.Fprintf(c.out, "          %s\n", result.Errors[i])
		}
		fmt.Fprintln(c.out)
	}

	fmt.Fprintf(c.out, "  Applied: %d  Skipped: %d  Failed: %d\n",
		len(result.Applied), len(result.Skipped), len(result.Failed))
}

func (c *command) printFixJSON(result *drift.RemediationResult, plan *drift.RemediationPlan, _ error) error {
	type fixOutput struct {
		Plan   *drift.RemediationPlan   `json:"plan"`
		Result *drift.RemediationResult `json:"result,omitempty"`
	}

	out := fixOutput{Plan: plan}
	if result != nil {
		out.Result = result
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding fix output: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
