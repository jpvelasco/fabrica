// Package forecast implements "fabrica cost forecast": project the current
// monthly cost estimate over a time horizon (daily burn, horizon cost,
// annualized). Offline — same config-derived model as cost report.
package forecast

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const defaultDays = 30

const caveat = "Note: estimates reflect current fabrica.yaml; run `<module> status` to reconcile."

type command struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	days      int
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

// New returns the "cost forecast" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "forecast",
		Short: "Project the current monthly estimate over a time horizon",
		Long: `Project the current estimated monthly cost over a time horizon: daily burn
rate, total cost over the horizon, and annualized cost. Offline — uses the same
config-derived estimate as cost report.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				days:      days,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
	cmd.Flags().IntVar(&days, "days", defaultDays, "forecast horizon in days")
	return cmd
}

func (c command) run() error {
	days := c.days
	if days <= 0 {
		days = defaultDays
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	f := fabricacost.Project(b.Total, days, b.Confidence)
	if c.jsonOut {
		return c.renderJSON(f)
	}
	f.Render(c.out)
	fmt.Fprintln(c.out, caveat)
	return nil
}

func (c command) renderJSON(f fabricacost.Forecast) error {
	payload := struct {
		MonthlyEstimate float64 `json:"monthlyEstimate"`
		Days            int     `json:"days"`
		DailyBurn       float64 `json:"dailyBurn"`
		HorizonCost     float64 `json:"horizonCost"`
		Annualized      float64 `json:"annualized"`
		Confidence      string  `json:"confidence"`
		Note            string  `json:"note"`
	}{
		MonthlyEstimate: f.MonthlyEstimate,
		Days:            f.Days,
		DailyBurn:       f.DailyBurn,
		HorizonCost:     f.HorizonCost,
		Annualized:      f.Annualized,
		Confidence:      f.Confidence.String(),
		Note:            caveat,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
