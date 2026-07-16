// Package destroyall orchestrates a full-stack teardown for `fabrica destroy
// --all`: it runs a caller-supplied, ordered set of per-module teardown
// closures, then deletes the state backend — but only if every module
// succeeded. A module failure is collected (not fatal mid-run); remaining
// modules are still torn down, and the backend is preserved so orphaned
// resources stay tracked for a clean retry.
package destroyall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

const lineWidth = 62

// ModuleTeardown tears down one module, returning the deleted resource IDs or
// an error. The caller (cmd/destroy) builds these closures over each module's
// teardown command with confirmation already skipped.
type ModuleTeardown func(ctx context.Context) ([]string, error)

// Module pairs a name with its teardown closure. Order in the slice is the
// execution order.
type Module struct {
	Name     string
	Teardown ModuleTeardown
}

// ModuleResult is the outcome of one module's teardown.
type ModuleResult struct {
	Module    string   `json:"module"`
	Destroyed []string `json:"destroyed"`
	Error     string   `json:"error,omitempty"`
}

// Result is the aggregate outcome of a destroy --all run.
type Result struct {
	Modules        []ModuleResult `json:"modules"`
	BackendDeleted bool           `json:"backendDeleted"`
	DryRun         bool           `json:"dryRun"`
}

// Engine runs the orchestrated teardown. Seam fields (Backend, Confirm) are
// wired to real implementations by cmd/destroy and replaced with fakes in tests.
type Engine struct {
	Account string
	Region  string
	Bucket  string
	Table   string
	Modules []Module

	Backend cloud.StateBackendDestroyer

	DryRun    bool
	AssumeYes bool
	JSONOut   bool
	Out       io.Writer

	Confirm func(msg, phrase string) bool
}

// Run executes the orchestrated teardown.
func (e Engine) Run(ctx context.Context) error {
	if len(e.Modules) == 0 && e.Bucket == "" && e.Table == "" {
		if e.JSONOut {
			e.printJSON(Result{Modules: []ModuleResult{}, DryRun: e.DryRun})
			return nil
		}
		fmt.Fprintln(e.Out, "No provisioned modules or state backend found. Nothing to destroy.")
		return nil
	}

	if e.DryRun {
		return e.runDryRun()
	}

	if !e.confirmAggregate() {
		return nil
	}

	return e.execute(ctx)
}

func (e Engine) runDryRun() error {
	if e.JSONOut {
		mods := make([]ModuleResult, 0, len(e.Modules))
		for _, m := range e.Modules {
			mods = append(mods, ModuleResult{Module: m.Name})
		}
		e.printJSON(Result{Modules: mods, DryRun: true})
		return nil
	}
	fmt.Fprintln(e.Out, "Destroy --all dry run")
	fmt.Fprintln(e.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(e.Out, "  Account: %s\n  Region:  %s\n\n", e.Account, e.Region)
	fmt.Fprintf(e.Out, "The following %d module(s) WOULD BE DELETED (in order):\n", len(e.Modules))
	if len(e.Modules) == 0 {
		fmt.Fprintln(e.Out, "  (none provisioned)")
	}
	for i, m := range e.Modules {
		fmt.Fprintf(e.Out, "  %d. %s (all its resources)\n", i+1, m.Name)
	}
	if e.Bucket != "" || e.Table != "" {
		fmt.Fprintln(e.Out, "\nThen the state backend WOULD BE DELETED:")
		fmt.Fprintf(e.Out, "  S3 bucket:      %s\n", e.Bucket)
		fmt.Fprintf(e.Out, "  DynamoDB table: %s\n", e.Table)
		fmt.Fprintln(e.Out, "  (backend is deleted only if every module above succeeds)")
	} else {
		fmt.Fprintln(e.Out, "\nNo state backend to delete.")
	}
	fmt.Fprintln(e.Out, "\nNothing has been deleted. Run without --dry-run to proceed.")
	return nil
}

func (e Engine) confirmAggregate() bool {
	if e.AssumeYes {
		if !e.JSONOut {
			fmt.Fprintln(e.Out, "Proceeding without interactive confirmation (--yes flag set).")
		}
		return true
	}
	phrase := fmt.Sprintf("destroy all %s", e.Account)
	fmt.Fprintln(e.Out, "Destroy --all plan")
	fmt.Fprintln(e.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(e.Out, "  Account: %s\n  Region:  %s\n\n", e.Account, e.Region)
	fmt.Fprintln(e.Out, "This will permanently delete ALL provisioned modules and the state backend.")
	fmt.Fprintln(e.Out, "Modules (in order):")
	for i, m := range e.Modules {
		fmt.Fprintf(e.Out, "  %d. %s\n", i+1, m.Name)
	}
	fmt.Fprintf(e.Out, "\nType this exact phrase to continue:\n\n  %s\n\n", phrase)
	if !e.Confirm("Enter confirmation phrase", phrase) {
		fmt.Fprintln(e.Out, "Cancelled. No AWS calls were made.")
		return false
	}
	fmt.Fprintln(e.Out, "Confirmation accepted.")
	return true
}

func (e Engine) execute(ctx context.Context) error {
	res := Result{Modules: make([]ModuleResult, 0, len(e.Modules))}
	anyFailed := false

	for _, m := range e.Modules {
		if !e.JSONOut {
			fmt.Fprintf(e.Out, "\n=== Tearing down %s ===\n", m.Name)
		}
		ids, err := m.Teardown(ctx)
		mr := ModuleResult{Module: m.Name, Destroyed: ids}
		if err != nil {
			mr.Error = err.Error()
			anyFailed = true
			if !e.JSONOut {
				fmt.Fprintf(e.Out, "  ERROR tearing down %s: %v\n", m.Name, err)
			}
		}
		res.Modules = append(res.Modules, mr)
	}

	if anyFailed {
		return e.finishWithFailure(res)
	}

	if err := e.deleteBackend(ctx, &res); err != nil {
		if e.JSONOut {
			e.printJSON(res)
		}
		return err
	}

	if e.JSONOut {
		e.printJSON(res)
		return nil
	}
	fmt.Fprintln(e.Out, "\nDestroy --all complete. All modules and the state backend were removed.")
	return nil
}

func (e Engine) finishWithFailure(res Result) error {
	failed := make([]string, 0)
	for _, m := range res.Modules {
		if m.Error != "" {
			failed = append(failed, m.Module)
		}
	}
	if e.JSONOut {
		e.printJSON(res)
	} else {
		fmt.Fprintln(e.Out, "\nDestroy --all did not complete: the following module(s) failed:")
		for _, m := range res.Modules {
			if m.Error != "" {
				fmt.Fprintf(e.Out, "  - %s: %s\n", m.Module, m.Error)
			}
		}
		fmt.Fprintln(e.Out, "The state backend was PRESERVED so orphaned resources stay tracked.")
		fmt.Fprintf(e.Out, "Retry the failed module(s) — e.g. 'fabrica %s destroy' — then re-run 'fabrica destroy --all'.\n", failed[0])
	}
	return fmt.Errorf("destroy --all incomplete: %d module(s) failed: %s", len(failed), strings.Join(failed, ", "))
}

func (e Engine) deleteBackend(ctx context.Context, res *Result) error {
	if e.Bucket == "" && e.Table == "" {
		return nil
	}
	if e.Backend == nil {
		return fmt.Errorf("provider does not support state backend destroy")
	}
	if !e.JSONOut {
		fmt.Fprintln(e.Out, "\n=== Deleting state backend ===")
	}
	if e.Bucket != "" {
		b, err := e.Backend.DeleteStateBucket(ctx, e.Bucket)
		if err != nil {
			e.printBackendFailure("S3 state bucket", e.Bucket, err)
			return fmt.Errorf("deleting state bucket %s: %w", e.Bucket, err)
		}
		e.printBackendResult("S3 state bucket", b)
	}
	if e.Table != "" {
		tbl, err := e.Backend.DeleteStateLockTable(ctx, e.Table)
		if err != nil {
			e.printBackendFailure("DynamoDB lock table", e.Table, err)
			return fmt.Errorf("deleting lock table %s: %w", e.Table, err)
		}
		e.printBackendResult("DynamoDB lock table", tbl)
	}
	res.BackendDeleted = true
	return nil
}

func (e Engine) printBackendResult(label string, r cloud.StateBackendDeleteResult) {
	if e.JSONOut {
		return
	}
	switch {
	case r.Deleted:
		fmt.Fprintf(e.Out, "  deleted %s: %s\n", label, r.Identifier)
	case r.Missing:
		fmt.Fprintf(e.Out, "  %s not found; skipping: %s\n", label, r.Identifier)
	default:
		fmt.Fprintf(e.Out, "  %s unchanged: %s\n", label, r.Identifier)
	}
}

func (e Engine) printBackendFailure(label, id string, err error) {
	if e.JSONOut {
		return
	}
	fmt.Fprintf(e.Out, "  failed to delete %s: %s\n", label, id)
	fmt.Fprintf(e.Out, "  Error: %v\n", err)
}

func (e Engine) printJSON(res Result) {
	data, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(e.Out, string(data))
}
