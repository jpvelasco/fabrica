package backup

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

func newDelete(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <backup-id>",
		Short: "Delete a Perforce backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := deleteCommand{
				runtime:   rt,
				assumeYes: opts.AssumeYes,
				dryRun:    opts.DryRun,
				backupID:  args[0],
				out:       out,
				confirm:   prompt.Confirm,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			if rt.Provider != nil {
				if rr, ok := rt.Provider.(cloud.RemoteRunner); ok {
					c.runRemote = rr.RunCommand
				}
			}
			return c.run(cmd.Context())
		},
	}
}

type deleteCommand struct {
	runtime   globals.Runtime
	assumeYes bool
	dryRun    bool
	backupID  string
	out       io.Writer
	confirm   func(string) bool

	readState  func() (*fabricastate.State, error)
	writeState func(*fabricastate.State) error
	runRemote  func(ctx context.Context, instanceID string, commands []string) (cloud.RemoteResult, error)
}

func (c deleteCommand) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("Perforce is not provisioned. Run 'fabrica perforce create' first")
	}
	inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || inst.Identifier == "" {
		return fmt.Errorf("Perforce instance not found in state")
	}

	cfg := perforceBackupCfg(c.runtime.Config)
	backupRoot := perforce.ResolveBackupPath(cfg.Path)

	if c.dryRun {
		fmt.Fprintf(c.out, "Would delete backup %s under %s (and S3 copy if metadata has s3Uri)\n", c.backupID, backupRoot)
		return nil
	}

	if !c.assumeYes {
		fmt.Fprintf(c.out, "This will permanently delete backup %s (local and S3 if exported).\n", c.backupID)
		if !c.confirm("Delete backup?") {
			fmt.Fprintln(c.out, "Cancelled.")
			return nil
		}
	}

	if c.runRemote == nil {
		return fmt.Errorf("provider does not support remote commands (SSM)")
	}

	// Read metadata for optional S3 URI.
	s3URI := ""
	metaScript, err := perforce.GenerateReadMetaScript(backupRoot, c.backupID)
	if err != nil {
		return err
	}
	metaRes, err := c.runRemote(ctx, inst.Identifier, []string{metaScript})
	if err == nil {
		if meta, perr := perforce.ParseBackupMeta([]byte(strings.TrimSpace(metaRes.Stdout))); perr == nil {
			s3URI = meta.S3URI
		}
	}

	delScript, err := perforce.GenerateDeleteScript(backupRoot, c.backupID, s3URI)
	if err != nil {
		return err
	}
	res, err := c.runRemote(ctx, inst.Identifier, []string{delScript})
	if err != nil {
		return fmt.Errorf("delete remote command failed: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("delete script exit %d: %s", res.ExitCode, res.Stderr)
	}

	// Clear last-backup props if this was the cached id.
	for i := range m.Resources {
		if m.Resources[i].TypeName == "AWS::EC2::Instance" && m.Resources[i].Properties != nil {
			if m.Resources[i].Properties["lastBackupId"] == c.backupID {
				delete(m.Resources[i].Properties, "lastBackupId")
				delete(m.Resources[i].Properties, "lastBackupAt")
			}
		}
	}
	st.UpsertModule(moduleName, m.Version, m.Status, m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	fmt.Fprintf(c.out, "Deleted backup %s\n", c.backupID)
	return nil
}
