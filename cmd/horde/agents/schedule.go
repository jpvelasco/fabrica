package agents

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/schedule"
	"github.com/spf13/cobra"
)

func newSchedule(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "schedule",
		Short: "Show agent Spot and weekly on/off schedule",
		Long: `Print horde.agents.spot and horde.agents.schedule. Fabrica does not
install EventBridge — wire the printed start/stop hints yourself.
Destroy still tears the pool down regardless of schedule.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			return schedule.PrintPlan(out, optionsSource().JSONOutput, rt.Config.Horde.Agents.Spot, rt.Config.Horde.Agents.Schedule,
				"fabrica horde agents create --yes", "scale desired to 0 or fabrica horde agents destroy --yes")
		},
	}
}
