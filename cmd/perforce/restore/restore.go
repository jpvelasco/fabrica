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
	st, m, inst, err := c.validateProvisioning()
	if err != nil {
		return err
	}
	if m.Status == "ready" && !c.force {
		return fmt.Errorf("Perforce is ready and may have connected clients. Re-run with --force to stop Helix Core, restore from the backup, and restart (clients will disconnect). Example: fabrica perforce restore %s --force", c.backupID)
	}

	backupRoot := c.resolveBackupRoot()
	if c.dryRun {
		c.printDryRun(backupRoot)
		return nil
	}

	if err := c.checkRemoteSupport(); err != nil {
		return err
	}

	meta, err := c.readBackupMetadata(ctx, inst.Identifier, backupRoot)
	if err != nil {
		return err
	}

	c.printRestorePlan(meta)
	if cancelled := c.confirmRestore(st); cancelled != nil {
		return nil // user cancelled; not an error
	}

	script, err := c.buildRestoreScript(backupRoot, meta)
	if err != nil {
		return err
	}

	if err := c.executeRestore(ctx, inst.Identifier, script); err != nil {
		return err
	}

	status, reachable := c.probeAndDetermineStatus(ctx, inst.Identifier, m.Status)
	if !reachable {
		fmt.Fprintln(c.out, "Warning: could not confirm Helix Core is reachable from this machine (check VPN/network).")
	}
	c.updateState(st, m, status)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Restore complete from %s\n", c.backupID)
	fmt.Fprintln(c.out, "Next: fabrica perforce status")
	return nil
}

func (c command) validateProvisioning() (*fabricastate.State, *fabricastate.ModuleState, *fabricastate.ModuleResource, error) {
	st, err := c.readState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return nil, nil, nil, fmt.Errorf("Perforce is not provisioned. Run 'fabrica perforce create' first")
	}
	inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || inst.Identifier == "" {
		return nil, nil, nil, fmt.Errorf("Perforce instance not found in state")
	}
	return st, m, &inst, nil
}

func (c command) resolveBackupRoot() string {
	cfg := c.runtime.Config.Perforce.Backup
	return perforce.ResolveBackupPath(cfg.Path)
}

func (c command) printDryRun(backupRoot string) {
	fmt.Fprintln(c.out, "Perforce restore (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Backup ID: %s\n", c.backupID)
	fmt.Fprintf(c.out, "  Source:    %s\n", perforce.BackupDir(backupRoot, c.backupID))
	fmt.Fprintln(c.out, "  Steps:     stop helix-p4d → restore checkpoint → start helix-p4d")
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) checkRemoteSupport() error {
	if c.runRemote == nil {
		return fmt.Errorf("provider does not support remote commands (SSM)")
	}
	return nil
}

func (c command) readBackupMetadata(ctx context.Context, instanceID string, backupRoot string) (perforce.BackupMeta, error) {
	metaScript, err := perforce.GenerateReadMetaScript(backupRoot, c.backupID)
	if err != nil {
		return perforce.BackupMeta{}, err
	}
	metaRes, err := c.runRemote(ctx, instanceID, []string{metaScript})
	if err != nil {
		return perforce.BackupMeta{}, fmt.Errorf("reading backup metadata: %w — verify backup id with 'fabrica perforce backup list'", err)
	}
	meta, err := perforce.ParseBackupMeta([]byte(strings.TrimSpace(metaRes.Stdout)))
	if err != nil {
		return perforce.BackupMeta{}, err
	}
	if meta.Status != perforce.BackupStatusComplete {
		return perforce.BackupMeta{}, fmt.Errorf("backup %s status is %q; only complete backups can be restored", c.backupID, meta.Status)
	}
	return meta, nil
}

func (c command) printRestorePlan(meta perforce.BackupMeta) {
	fmt.Fprintln(c.out, "Perforce restore")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Backup ID:   %s\n", meta.ID)
	fmt.Fprintf(c.out, "  Created:     %s\n", meta.CreatedAt)
	fmt.Fprintf(c.out, "  Helix:       %s\n", meta.HelixVersion)
	fmt.Fprintf(c.out, "  Size bytes:  %d\n", meta.SizeBytes)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "IRREVERSIBLE: This stops Helix Core and replaces server data from the backup.")
}

func (c command) confirmRestore(st *fabricastate.State) error {
	if c.assumeYes {
		return nil
	}
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
		return fmt.Errorf("cancelled by user")
	}
	return nil
}

func (c command) buildRestoreScript(backupRoot string, meta perforce.BackupMeta) (string, error) {
	adminPass, err := c.readCreds()
	if err != nil {
		return "", err
	}
	return perforce.GenerateRestoreScript(perforce.RestoreScriptConfig{
		BackupID:      c.backupID,
		BackupRoot:    backupRoot,
		ServerRoot:    meta.ServerRoot,
		AdminPassword: adminPass,
	})
}

func (c command) executeRestore(ctx context.Context, instanceID string, script string) error {
	fmt.Fprintln(c.out, "Running restore via SSM...")
	res, err := c.runRemote(ctx, instanceID, []string{script})
	if err != nil {
		return fmt.Errorf("restore remote command failed: %w\nstderr: %s", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("restore script exit %d: %s", res.ExitCode, res.Stderr)
	}
	return nil
}

func (c command) probeAndDetermineStatus(ctx context.Context, instanceID string, currentStatus string) (string, bool) {
	if c.getResource == nil || c.probeTCP == nil {
		return currentStatus, false
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instanceID}
	if err := c.getResource(ctx, r); err != nil || len(r.ActualState) == 0 {
		return currentStatus, false
	}
	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil || actual.PrivateIPAddress == "" {
		return currentStatus, false
	}

	time.Sleep(2 * time.Second)
	if c.probeTCP(fmt.Sprintf("%s:%d", actual.PrivateIPAddress, p4Port)) {
		return "ready", true
	}
	return currentStatus, false
}

func (c command) updateState(st *fabricastate.State, m *fabricastate.ModuleState, status string) {
	st.UpsertModule(moduleName, m.Version, status, m.Resources)
	st.AddOp(moduleName, "restore", 1)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
}
