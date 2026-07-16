// Package report implements "fabrica cost report": an offline monthly cost
// estimate broken down by provisioned module. It prefers the deployed shape
// recorded in local state (instance type, volume size, fleet size) and falls
// back to the current fabrica.yaml for modules that predate that backfill.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const lineWidth = 64

const caveat = "Note: estimates use deployed state where recorded, else current fabrica.yaml; run `<module> status` to reconcile."

type command struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

// New returns the "cost report" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show the estimated monthly cost broken down by module",
		Long: `Show the estimated monthly infrastructure cost, broken down by provisioned
module and resource. Fully offline, no AWS calls: reads local state for which
modules exist and their deployed shape (instance type, volume/fleet size),
falling back to the current fabrica.yaml for cost inputs not recorded in state.`,
		Example: `  # Estimated monthly cost by module:
  fabrica cost report

  # Machine-readable breakdown for scripts:
  fabrica cost report --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
}

func (c command) run() error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	if c.jsonOut {
		return c.renderJSON(b)
	}
	c.renderText(b)
	return nil
}

func (c command) renderText(b costsource.Breakdown) {
	divider := strings.Repeat("-", lineWidth)
	fmt.Fprintln(c.out, "Cost estimate (monthly) — from deployed state, fabrica.yaml where unrecorded")
	fmt.Fprintln(c.out, divider)
	if len(b.Modules) == 0 {
		fmt.Fprintln(c.out, "  No provisioned modules found in state.")
	}
	for _, m := range b.Modules {
		fmt.Fprintf(c.out, "  %-14s (%s)\n", m.Name, m.Status)
		for _, r := range m.Report.Results {
			if r.Err != nil {
				fmt.Fprintf(c.out, "    %-22s %10s  %s\n", r.Resource.Name, "-", "(no estimate)")
				continue
			}
			fmt.Fprintf(c.out, "    %-22s $%9.2f  %s\n", r.Resource.Name, r.Monthly.Amount, r.Monthly.Confidence)
		}
		if m.Note != "" {
			fmt.Fprintf(c.out, "    (%s)\n", m.Note)
		}
		fmt.Fprintf(c.out, "    %-22s $%9.2f\n", "subtotal", m.Subtotal)
	}
	fmt.Fprintln(c.out, divider)
	fmt.Fprintf(c.out, "  %-22s $%9.2f\n", "Total:", b.Total)
	fmt.Fprintf(c.out, "Confidence: %s\n", b.Confidence)
	fmt.Fprintln(c.out, caveat)
}

// jsonModule is the JSON shape for one module in the report.
type jsonModule struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Subtotal float64 `json:"subtotal"`
	Note     string  `json:"note,omitempty"`
}

func (c command) renderJSON(b costsource.Breakdown) error {
	payload := struct {
		Total      float64      `json:"total"`
		Confidence string       `json:"confidence"`
		Modules    []jsonModule `json:"modules"`
		Note       string       `json:"note"`
	}{
		Total:      b.Total,
		Confidence: b.Confidence.String(),
		Note:       caveat,
	}
	for _, m := range b.Modules {
		payload.Modules = append(payload.Modules, jsonModule{
			Name: m.Name, Status: m.Status, Subtotal: m.Subtotal, Note: m.Note,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
