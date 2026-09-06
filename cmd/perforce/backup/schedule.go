package backup

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/spf13/cobra"
)

func newSchedule(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "schedule",
		Short: "Show the configured backup schedule and DR runbook",
		Long: `Print the Perforce backup schedule from fabrica.yaml and the restore/verify
runbook. Fabrica does not install cron or EventBridge — wire
` + "`fabrica perforce backup --yes`" + ` to the cron yourself.

Set perforce.backup.schedule (5-field cron) and optional retain to enable
the runbook and the backup-storage cost line.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			return runSchedule(rt, opts.JSONOutput, out)
		},
	}
}

func runSchedule(rt globals.Runtime, jsonOut bool, out io.Writer) error {
	plan, err := perforce.BuildSchedulePlan(rt.Config.Perforce.Backup)
	if err != nil {
		return err
	}
	if jsonOut {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	if !plan.Enabled {
		fmt.Fprintln(out, "Perforce backup schedule: disabled")
		fmt.Fprintln(out, plan.Note)
		return nil
	}
	fmt.Fprintf(out, "Perforce backup schedule: %s\n", plan.Cron)
	fmt.Fprintf(out, "  retain:  %d\n", plan.Retain)
	fmt.Fprintf(out, "  path:    %s\n", plan.Path)
	fmt.Fprintf(out, "  command: %s\n", plan.Command)
	fmt.Fprintf(out, "  verify:  %s\n", plan.Verify)
	fmt.Fprintf(out, "  restore: %s\n", plan.Restore)
	if plan.S3Export {
		fmt.Fprintf(out, "  s3:      %s\n", plan.S3Bucket)
	}
	fmt.Fprintln(out, plan.Note)
	return nil
}
