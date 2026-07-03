// Package alerts implements "fabrica cost alerts": manage and evaluate local
// budget thresholds. list/check are read-only; set upserts a threshold into
// fabrica.yaml (honoring --dry-run). Thresholds are local guardrails only — no
// AWS Budgets resources are created.
package alerts

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// knownScopes are the valid budget scopes: "total" plus each module name.
var knownScopes = map[string]bool{
	"total": true, "perforce": true, "horde": true,
	"workstation": true, "ci": true, "deploy": true,
}

// New returns the "cost alerts" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Manage and check local budget thresholds",
		Long: `Manage local budget thresholds and check the current estimate against them.
Thresholds are local guardrails written to fabrica.yaml — no AWS Budgets
resources are created. cost alerts check is informational (exit code stays 0).`,
	}
	cmd.AddCommand(newList(runtimeSource, optionsSource, out))
	cmd.AddCommand(newSet(runtimeSource, optionsSource, out))
	cmd.AddCommand(newCheck(runtimeSource, optionsSource, out))
	return cmd
}

// ---- list ----

type listCommand struct {
	cfg     *config.Config
	jsonOut bool
	out     io.Writer
}

func newList(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show configured budget thresholds",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := listCommand{cfg: rt.Config, jsonOut: optionsSource().JSONOutput, out: out}
			return c.run()
		},
	}
}

func (c listCommand) run() error {
	budgets := c.cfg.Cost.Budgets
	if c.jsonOut {
		data, err := json.MarshalIndent(budgets, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	if len(budgets) == 0 {
		fmt.Fprintln(c.out, "No budget thresholds configured. Set one with: fabrica cost alerts set <scope> <monthly>")
		return nil
	}
	fmt.Fprintln(c.out, "Configured budget thresholds:")
	for _, b := range budgets {
		warn := b.WarnPct
		if warn <= 0 {
			warn = 80
		}
		fmt.Fprintf(c.out, "  %-12s $%-9.2f (warn at %d%%)\n", b.Scope, b.Monthly, warn)
	}
	return nil
}

// ---- set ----

type setCommand struct {
	cfg     *config.Config
	dryRun  bool
	out     io.Writer
	cfgPath string
	cfgSave func(*config.Config, string) error
}

func newSet(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var warnPct int
	cmd := &cobra.Command{
		Use:   "set <scope> <monthly>",
		Short: "Configure a budget threshold (writes fabrica.yaml)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			monthly, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return fmt.Errorf("invalid monthly amount %q: must be a number (USD/month)", args[1])
			}
			c := setCommand{
				cfg:     rt.Config,
				dryRun:  opts.DryRun,
				out:     out,
				cfgPath: rt.ConfigFile(),
				cfgSave: func(cc *config.Config, path string) error { return cc.Save(path) },
			}
			return c.run(args[0], monthly, warnPct)
		},
	}
	cmd.Flags().IntVar(&warnPct, "warn-pct", 0, "warn threshold as percent of monthly (0 = default 80)")
	return cmd
}

func (c setCommand) run(scope string, monthly float64, warnPct int) error {
	if monthly <= 0 {
		return fmt.Errorf("monthly budget must be greater than 0 (got %v) — pass a positive USD amount", monthly)
	}
	if !knownScopes[scope] {
		return fmt.Errorf("unknown scope %q — must be \"total\" or a module name (perforce, horde, workstation, ci, deploy)", scope)
	}
	// Upsert into a copy so dry-run never mutates shared config.
	updated := upsert(c.cfg.Cost.Budgets, config.BudgetThreshold{Scope: scope, Monthly: monthly, WarnPct: warnPct})

	if c.dryRun {
		fmt.Fprintf(c.out, "Would set budget: %s = $%.2f", scope, monthly)
		if warnPct > 0 {
			fmt.Fprintf(c.out, " (warn at %d%%)", warnPct)
		}
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Dry run — fabrica.yaml not modified.")
		return nil
	}
	c.cfg.Cost.Budgets = updated
	if err := c.cfgSave(c.cfg, c.cfgPath); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(c.out, "Set budget: %s = $%.2f\n", scope, monthly)
	return nil
}

// upsert replaces the threshold for a scope if present, else appends it.
func upsert(budgets []config.BudgetThreshold, b config.BudgetThreshold) []config.BudgetThreshold {
	out := make([]config.BudgetThreshold, len(budgets))
	copy(out, budgets)
	for i := range out {
		if out[i].Scope == b.Scope {
			out[i] = b
			return out
		}
	}
	return append(out, b)
}

// ---- check ----

type checkCommand struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

func newCheck(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Evaluate the current estimate against configured thresholds",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := checkCommand{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				jsonOut:   optionsSource().JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
}

func (c checkCommand) run() error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	statuses := fabricacost.EvaluateBudgets(b.PerScope, costsource.MapBudgets(c.cfg.Cost.Budgets))
	// Deterministic order for stable output.
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Scope < statuses[j].Scope })
	if c.jsonOut {
		return c.renderJSON(statuses)
	}
	if len(statuses) == 0 {
		fmt.Fprintln(c.out, "No budget thresholds configured. Set one with: fabrica cost alerts set <scope> <monthly>")
		return nil
	}
	fabricacost.RenderBudgets(c.out, statuses)
	return nil
}

func (c checkCommand) renderJSON(statuses []fabricacost.BudgetStatus) error {
	type jsonStatus struct {
		Scope     string  `json:"scope"`
		Estimate  float64 `json:"estimate"`
		Threshold float64 `json:"threshold"`
		WarnPct   int     `json:"warnPct"`
		State     string  `json:"state"`
		NoMatch   bool    `json:"noMatch"`
	}
	out := make([]jsonStatus, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, jsonStatus{
			Scope: s.Scope, Estimate: s.Estimate, Threshold: s.Threshold,
			WarnPct: s.WarnPct, State: s.State.String(), NoMatch: s.NoMatch,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
