package ddc

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/spf13/cobra"
)

func newTopology(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "topology",
		Short: "Show production Scylla and replication-peer plan",
		Long: `Print the configured DDC Scylla topology (node count, RF) and replication
peers. V1 still provisions one Scylla host; extra nodes stay operator-built.
Peers are written into fabrica.env when ddc.replication.enabled is true.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			plan, err := ddc.BuildTopologyPlan(rt.Config.DDC)
			if err != nil {
				return err
			}
			if optionsSource().JSONOutput {
				data, err := json.MarshalIndent(plan, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}
			fmt.Fprintf(out, "Backend:     %s\n", plan.Backend)
			fmt.Fprintf(out, "Scylla:      %d nodes, RF=%d\n", plan.ScyllaNodes, plan.RF)
			if plan.Datacenter != "" {
				fmt.Fprintf(out, "Datacenter:  %s\n", plan.Datacenter)
			}
			if len(plan.Peers) > 0 {
				fmt.Fprintf(out, "Peers:       %s\n", strings.Join(plan.Peers, ", "))
			}
			fmt.Fprintf(out, "Production:  %v\n", plan.Production)
			fmt.Fprintln(out, plan.Note)
			return nil
		},
	}
}
