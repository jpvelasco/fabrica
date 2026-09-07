package agents

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

type queueMetrics struct {
	MetricName      string  `json:"metricName"`
	MetricNamespace string  `json:"metricNamespace"`
	ScaleOut        float64 `json:"scaleOutThreshold"`
	ScaleIn         float64 `json:"scaleInThreshold"`
	Note            string  `json:"note"`
}

func newMetrics(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Show Horde agent queue-depth metric names",
		Long: `Print the CloudWatch metric name/namespace agents must publish for
queue-based autoscaling (default ASGQueueDepth in Fabrica/HordeAgents).
Fabrica does not scrape Horde; agents publish the metric themselves.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			cfg := rt.Config.Horde.Agents.Scaling
			name := cfg.MetricName
			if name == "" {
				name = "ASGQueueDepth"
			}
			ns := cfg.MetricNamespace
			if ns == "" {
				ns = "Fabrica/HordeAgents"
			}
			doc := queueMetrics{
				MetricName:      name,
				MetricNamespace: ns,
				ScaleOut:        cfg.ScaleOutThreshold,
				ScaleIn:         cfg.ScaleInThreshold,
				Note:            "Agents must PutMetricData this metric. Fabrica only provisions alarms/policies.",
			}
			if optionsSource().JSONOutput {
				b, err := json.MarshalIndent(doc, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(b))
				return nil
			}
			fmt.Fprintf(out, "Queue metric: %s / %s\n", doc.MetricNamespace, doc.MetricName)
			fmt.Fprintf(out, "Scale-out:    %g\n", doc.ScaleOut)
			fmt.Fprintf(out, "Scale-in:     %g\n", doc.ScaleIn)
			fmt.Fprintln(out, doc.Note)
			return nil
		},
	}
}
