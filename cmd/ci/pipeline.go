package ci

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/spf13/cobra"
)

func newPipeline(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "pipeline",
		Short: "Show the optional CodePipeline overlay",
		Long: `Print the documented CodePipeline path for studio CI. V1 does not
provision a pipeline; fabrica ci trigger still starts CodeBuild.
Enable ci.pipeline to include the standing cost line.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			plan := ci.BuildPipelinePlan(rt.Config.CI)
			if optionsSource().JSONOutput {
				b, err := json.MarshalIndent(plan, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(b))
				return nil
			}
			fmt.Fprintf(out, "Pipeline: %s (enabled=%v)\n", plan.Name, plan.Enabled)
			fmt.Fprintf(out, "Stages:   %v\n", plan.Stages)
			fmt.Fprintln(out, plan.Note)
			return nil
		},
	}
}
