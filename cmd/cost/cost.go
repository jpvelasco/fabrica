// Package cost wires the "cost" parent command and its subcommands (report,
// forecast, alerts): offline, config-derived cost visibility and local budget
// guardrails. None of the subcommands require a live cloud provider.
package cost

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/cost/forecast"
	"github.com/jpvelasco/fabrica/cmd/cost/report"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "cost" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "Estimate and monitor infrastructure cost",
		Long: `Offline cost visibility and local budget guardrails across all provisioned
modules. Estimates are derived from the current fabrica.yaml, scoped to the
modules present in local state — no AWS calls.

Available operations:
  report    Estimated monthly cost broken down by module
  forecast  Project the current estimate over a time horizon
  alerts    Manage and check local budget thresholds`,
	}
	cmd.AddCommand(report.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(forecast.New(runtimeSource, optionsSource, out))
	return cmd
}
