// Package hordejob holds the Horde job flags shared by the commands that
// create jobs (horde submit, and ci trigger once it submits through Horde).
package hordejob

import (
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/spf13/cobra"
)

// BindFlags registers --stream, --template, --change, and --name on cmd and
// parses them into o. Flags override the horde.streamId, horde.templateId,
// and horde.change config keys; an unset flag leaves the config value in
// place.
func BindFlags(cmd *cobra.Command, o *horde.JobOverrides) {
	cmd.Flags().StringVar(&o.StreamID, "stream", "", "Horde stream ID to run the job in (overrides horde.streamId)")
	cmd.Flags().StringVar(&o.TemplateID, "template", "", "Horde job template ID (overrides horde.templateId)")
	cmd.Flags().IntVar(&o.Change, "change", 0, "Perforce changelist to build (overrides horde.change; 0 keeps the config value, or Horde's latest)")
	cmd.Flags().StringVar(&o.Name, "name", "", "Job name shown in the Horde web UI (default: the BuildGraph agent name, when a file is given)")
}
