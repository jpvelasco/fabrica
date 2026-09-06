package backup

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/spf13/cobra"
)

func newVerify(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "verify <backup-id>",
		Short: "Print the documented verify steps for a backup id",
		Long: `Print the documented verify + restore path for a backup id.

V1 does not open an SSM session. It confirms the id shape and prints the
operator runbook: list, inspect metadata, then restore.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			return runVerify(rt, args[0], opts.JSONOutput, out)
		},
	}
}

func runVerify(rt globals.Runtime, backupID string, jsonOut bool, out io.Writer) error {
	id := perforce.SanitizeBackupName(backupID)
	if id == "" {
		return fmt.Errorf("backup id is empty after sanitizing %q — use an id from `fabrica perforce backup list`", backupID)
	}
	path := perforce.ResolveBackupPath(rt.Config.Perforce.Backup.Path)
	doc := map[string]string{
		"backupId": id,
		"path":     path + "/" + id,
		"list":     "fabrica perforce backup list",
		"restore":  "fabrica perforce restore " + id,
		"note":     "Confirm metadata.json exists on the instance (or S3), then restore. Verify does not mutate state.",
	}
	if jsonOut {
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	fmt.Fprintf(out, "Verify backup %s\n", id)
	fmt.Fprintf(out, "  path:    %s\n", doc["path"])
	fmt.Fprintf(out, "  list:    %s\n", doc["list"])
	fmt.Fprintf(out, "  restore: %s\n", doc["restore"])
	fmt.Fprintln(out, doc["note"])
	return nil
}
