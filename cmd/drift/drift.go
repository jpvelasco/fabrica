// Package driftcmd implements `fabrica drift`: a read-only drift detection
// command that compares recorded state against live AWS resources. It never
// mutates state or cloud resources.
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
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const lineWidth = 64

type command struct {
	runtime     globals.Runtime
	jsonOut     bool
	out         io.Writer
	readState   func() (*fabricastate.State, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
	backend     cloud.StateBackendChecker
	codebuild   cloud.CodeBuildRunner
}

// New returns the "fabrica drift" command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Detect drift between recorded state and live AWS resources",
		Long: `Compare recorded state (.fabrica/state.json) against live AWS resources
and report whether each resource is in sync, missing, or has attribute
mismatches.

This command is read-only: it never modifies state or cloud resources.

Checks the state backend (S3 bucket, DynamoDB table), EC2 instances
(existence, state, instance type, AMI), security groups, IAM roles,
and CodeBuild projects.`,
		Example: `  # Check drift for all provisioned modules:
  fabrica drift

  # Machine-readable output for scripts:
  fabrica drift --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
				if b, ok := rt.Provider.(cloud.StateBackendChecker); ok {
					c.backend = b
				}
				if cb, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.codebuild = cb
				}
			}
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c *command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	engine := &drift.Engine{
		State:           st,
		ResourceGet:     c.getResource,
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

	if c.jsonOut {
		return c.printJSON(report)
	}
	c.printText(report)
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
