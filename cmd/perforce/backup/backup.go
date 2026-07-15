// Package backup implements fabrica perforce backup (create), backup list, and backup delete.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
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
)

// New returns the "perforce backup" parent command. With no subcommand it creates a backup.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var name, description string
	var noS3 bool

	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create or manage Perforce backups",
		Long: `Create a consistent Perforce backup on the instance EBS volume (optional S3 export).

Requires the Perforce module to be ready and an SSM-managed instance profile
(attached by fabrica perforce create). Checkpoint briefly quiesces the server.

Subcommands:
  list     List backups on the server
  delete   Delete a backup by id

With no subcommand, creates a new backup.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := createCommand{
				runtime:     rt,
				dryRun:      opts.DryRun,
				assumeYes:   opts.AssumeYes,
				jsonOut:     opts.JSONOutput,
				name:        name,
				description: description,
				noS3:        noS3,
				out:         out,
				confirm:     prompt.Confirm,
				now:         time.Now,
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
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Optional short name appended to the backup id")
	cmd.Flags().StringVar(&description, "description", "", "Optional description stored in backup metadata")
	cmd.Flags().BoolVar(&noS3, "no-s3", false, "Skip S3 export even if perforce.backup.s3Export is true")

	cmd.AddCommand(newList(runtimeSource, optionsSource, out))
	cmd.AddCommand(newDelete(runtimeSource, optionsSource, out))
	return cmd
}

type createCommand struct {
	runtime     globals.Runtime
	dryRun      bool
	assumeYes   bool
	jsonOut     bool
	name        string
	description string
	noS3        bool
	out         io.Writer
	confirm     func(string) bool
	now         func() time.Time

	readState  func() (*fabricastate.State, error)
	writeState func(*fabricastate.State) error
	runRemote  func(ctx context.Context, instanceID string, commands []string) (cloud.RemoteResult, error)
	readCreds  func() (string, error)
}

type createOutput struct {
	BackupID string `json:"backupId"`
	Status   string `json:"status"`
	DryRun   bool   `json:"dryRun"`
	S3Export bool   `json:"s3Export"`
	Path     string `json:"path"`
}

func (c createCommand) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("Perforce is not provisioned. Run 'fabrica perforce create' first")
	}
	if m.Status != "ready" {
		return fmt.Errorf("Perforce status is %q; backups require status ready. Run 'fabrica perforce status' first", m.Status)
	}
	inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || inst.Identifier == "" {
		return fmt.Errorf("Perforce instance not found in state")
	}

	cfg := perforceBackupCfg(c.runtime.Config)
	backupRoot := perforce.ResolveBackupPath(cfg.Path)
	s3Export := cfg.S3Export && !c.noS3
	if s3Export && cfg.S3Bucket == "" {
		return fmt.Errorf("perforce.backup.s3Export is true but s3Bucket is empty — set perforce.backup.s3Bucket or pass --no-s3")
	}

	id := perforce.NewBackupID(c.now(), c.name)
	dest := perforce.BackupDir(backupRoot, id)

	if c.dryRun {
		return c.printDryRun(id, dest, s3Export, cfg)
	}

	fmt.Fprintln(c.out, "Perforce backup")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Backup ID:  %s\n", id)
	fmt.Fprintf(c.out, "  Path:       %s\n", dest)
	if s3Export {
		fmt.Fprintf(c.out, "  S3 export:  s3://%s/%s%s\n", cfg.S3Bucket, perforce.ResolveS3Prefix(cfg.S3Prefix), id)
		fmt.Fprintln(c.out, "  Note:       S3 storage is billed outside Fabrica cost estimates.")
	} else {
		fmt.Fprintln(c.out, "  S3 export:  disabled")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "WARNING: Checkpoint briefly quiesces Helix Core; clients may stall.")
	fmt.Fprintln(c.out)

	if !c.assumeYes {
		if !c.confirm("Create backup now?") {
			fmt.Fprintln(c.out, "Cancelled. No remote commands were run.")
			return nil
		}
	}

	if c.runRemote == nil {
		return fmt.Errorf("provider does not support remote commands (SSM). Use the AWS provider and ensure the instance has an SSM instance profile")
	}
	adminPass, err := c.readCreds()
	if err != nil {
		return err
	}

	script, err := perforce.GenerateBackupScript(perforce.BackupScriptConfig{
		BackupID:      id,
		BackupRoot:    backupRoot,
		HelixVersion:  m.Version,
		Name:          perforce.SanitizeBackupName(c.name),
		Description:   c.description,
		AdminPassword: adminPass,
		S3Export:      s3Export,
		S3Bucket:      cfg.S3Bucket,
		S3Prefix:      cfg.S3Prefix,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(c.out, "Running backup via SSM...")
	res, err := c.runRemote(ctx, inst.Identifier, []string{script})
	if err != nil {
		return fmt.Errorf("backup remote command failed: %w\nIf the instance has no SSM profile, recreate Perforce with a current Fabrica or attach AmazonSSMManagedInstanceCore and retry.\nstderr: %s", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("backup script exit %d: %s", res.ExitCode, res.Stderr)
	}

	// Update last-backup cache on instance properties.
	for i := range m.Resources {
		if m.Resources[i].TypeName == "AWS::EC2::Instance" {
			if m.Resources[i].Properties == nil {
				m.Resources[i].Properties = map[string]string{}
			}
			m.Resources[i].Properties["lastBackupId"] = id
			m.Resources[i].Properties["lastBackupAt"] = c.now().UTC().Format(time.RFC3339)
		}
	}
	st.UpsertModule(moduleName, m.Version, m.Status, m.Resources)
	st.AddOp(moduleName, "backup", 1)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		data, _ := json.MarshalIndent(createOutput{
			BackupID: id, Status: "complete", DryRun: false, S3Export: s3Export, Path: dest,
		}, "", "  ")
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Backup complete: %s\n", id)
	fmt.Fprintf(c.out, "  Stored at: %s\n", dest)
	return nil
}

func (c createCommand) printDryRun(id, dest string, s3Export bool, cfg config.PerforceBackupConfig) error {
	if c.jsonOut {
		data, _ := json.MarshalIndent(createOutput{
			BackupID: id, Status: "planned", DryRun: true, S3Export: s3Export, Path: dest,
		}, "", "  ")
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	fmt.Fprintln(c.out, "Perforce backup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Backup ID:  %s\n", id)
	fmt.Fprintf(c.out, "  Path:       %s\n", dest)
	if s3Export {
		fmt.Fprintf(c.out, "  S3 export:  s3://%s/%s%s\n", cfg.S3Bucket, perforce.ResolveS3Prefix(cfg.S3Prefix), id)
		fmt.Fprintln(c.out, "  Note:       S3 storage is billed outside Fabrica cost estimates.")
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "WARNING: Checkpoint briefly quiesces Helix Core; clients may stall.")
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
	return nil
}

func perforceBackupCfg(cfg *config.Config) config.PerforceBackupConfig {
	if cfg == nil {
		return config.PerforceBackupConfig{}
	}
	return cfg.Perforce.Backup
}
