// Package restore implements fabrica perforce restore.
package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "perforce"
	credFile   = ".fabrica/perforce-credentials.yaml"
	lineWidth  = 58
	p4Port     = 1666
)

// New returns the "perforce restore" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <backup-id>",
		Short: "Restore Perforce from a backup",
		Long: `Restore Helix Core from a backup on the instance EBS volume.

Stops helix-p4d, restores checkpoint/journal artifacts, and restarts the service.
When the server is ready (serving clients), --force is required.

Confirmation phrase: restore perforce <account-id>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				force:     force,
				backupID:  args[0],
				out:       out,
				confirm:   prompt.ConfirmExact,
				probeTCP:  modstatus.DefaultProbeTCP,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			c.readCreds = func() (string, error) {
				raw, err := credentials.ReadFile(credFile)
				if err != nil {
					return "", err
				}
				return credentials.ParsePerforceAdminPassword(raw)
			}
			if rt.Provider != nil {
				if rr, ok := rt.Provider.(cloud.RemoteRunner); ok {
					c.runRemote = rr.RunCommand
				}
				c.getResource = rt.Provider.Resources().Get
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Allow restore while the server is ready (disconnects clients)")
	return cmd
}

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	force     bool
	backupID  string
	out       io.Writer
	confirm   func(string, string) bool
	probeTCP  func(string) bool

	readState   func() (*fabricastate.State, error)
	writeState  func(*fabricastate.State) error
	runRemote   func(ctx context.Context, instanceID string, commands []string) (cloud.RemoteResult, error)
	readCreds   func() (string, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
}

func (c command) run(ctx context.Context) error {
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

	if m.Status == "ready" && !c.force {
		return fmt.Errorf("Perforce is ready and may have connected clients. Re-run with --force to stop Helix Core, restore from the backup, and restart (clients will disconnect). Example: fabrica perforce restore %s --force", c.backupID)
	}

	cfg := c.runtime.Config.Perforce.Backup
	backupRoot := perforce.ResolveBackupPath(cfg.Path)

	if c.dryRun {
		fmt.Fprintln(c.out, "Perforce restore (dry run)")
		fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
		fmt.Fprintf(c.out, "  Backup ID: %s\n", c.backupID)
		fmt.Fprintf(c.out, "  Source:    %s\n", perforce.BackupDir(backupRoot, c.backupID))
		fmt.Fprintln(c.out, "  Steps:     stop helix-p4d → restore checkpoint → start helix-p4d")
		fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
		return nil
	}

	if c.runRemote == nil {
		return fmt.Errorf("provider does not support remote commands (SSM)")
	}

	// Verify metadata exists and is complete.
	metaScript, err := perforce.GenerateReadMetaScript(backupRoot, c.backupID)
	if err != nil {
		return err
	}
	metaRes, err := c.runRemote(ctx, inst.Identifier, []string{metaScript})
	if err != nil {
		return fmt.Errorf("reading backup metadata: %w — verify backup id with 'fabrica perforce backup list'", err)
	}
	meta, err := perforce.ParseBackupMeta([]byte(strings.TrimSpace(metaRes.Stdout)))
	if err != nil {
		return err
	}
	if meta.Status != perforce.BackupStatusComplete {
		return fmt.Errorf("backup %s status is %q; only complete backups can be restored", c.backupID, meta.Status)
	}

	fmt.Fprintln(c.out, "Perforce restore")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Backup ID:   %s\n", meta.ID)
	fmt.Fprintf(c.out, "  Created:     %s\n", meta.CreatedAt)
	fmt.Fprintf(c.out, "  Helix:       %s\n", meta.HelixVersion)
	fmt.Fprintf(c.out, "  Size bytes:  %d\n", meta.SizeBytes)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "IRREVERSIBLE: This stops Helix Core and replaces server data from the backup.")

	if !c.assumeYes {
		account := ""
		if c.runtime.Config != nil {
			account = c.runtime.Config.Cloud.AWS.AccountID
		}
		if account == "" {
			account = st.Account
		}
		phrase := fmt.Sprintf("restore perforce %s", account)
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Type this exact phrase to continue:")
		fmt.Fprintf(c.out, "  %s\n", phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No remote commands were run.")
			return nil
		}
	}

	adminPass, err := c.readCreds()
	if err != nil {
		return err
	}
	script, err := perforce.GenerateRestoreScript(perforce.RestoreScriptConfig{
		BackupID:      c.backupID,
		BackupRoot:    backupRoot,
		ServerRoot:    meta.ServerRoot,
		AdminPassword: adminPass,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(c.out, "Running restore via SSM...")
	res, err := c.runRemote(ctx, inst.Identifier, []string{script})
	if err != nil {
		return fmt.Errorf("restore remote command failed: %w\nstderr: %s", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("restore script exit %d: %s", res.ExitCode, res.Stderr)
	}

	// Optional TCP probe via private IP.
	reachable := false
	if c.getResource != nil {
		r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: inst.Identifier}
		if err := c.getResource(ctx, r); err == nil && len(r.ActualState) > 0 {
			var actual struct {
				PrivateIPAddress string `json:"PrivateIpAddress"`
			}
			_ = json.Unmarshal(r.ActualState, &actual)
			if actual.PrivateIPAddress != "" && c.probeTCP != nil {
				// Brief wait for p4d to bind.
				time.Sleep(2 * time.Second)
				reachable = c.probeTCP(fmt.Sprintf("%s:%d", actual.PrivateIPAddress, p4Port))
			}
		}
	}

	status := "ready"
	if !reachable {
		status = m.Status
		fmt.Fprintln(c.out, "Warning: could not confirm Helix Core is reachable from this machine (check VPN/network).")
	}
	st.UpsertModule(moduleName, m.Version, status, m.Resources)
	st.AddOp(moduleName, "restore", 1)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Restore complete from %s\n", c.backupID)
	fmt.Fprintln(c.out, "Next: fabrica perforce status")
	return nil
}
