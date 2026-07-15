package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/perforce"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

func newList(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Perforce backups on the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := listCommand{
				runtime: rt,
				jsonOut: opts.JSONOutput,
				out:     out,
			}
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			if rt.Provider != nil {
				if rr, ok := rt.Provider.(cloud.RemoteRunner); ok {
					c.runRemote = rr.RunCommand
				}
			}
			return c.run(cmd.Context())
		},
	}
}

type listCommand struct {
	runtime   globals.Runtime
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
	runRemote func(ctx context.Context, instanceID string, commands []string) (cloud.RemoteResult, error)
}

func (c listCommand) run(ctx context.Context) error {
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
	if c.runRemote == nil {
		return fmt.Errorf("provider does not support remote commands (SSM)")
	}

	cfg := perforceBackupCfg(c.runtime.Config)
	script := perforce.GenerateListScript(cfg.Path)
	res, err := c.runRemote(ctx, inst.Identifier, []string{script})
	if err != nil {
		return fmt.Errorf("list remote command failed: %w", err)
	}
	list, err := perforce.ParseBackupMetaList(res.Stdout)
	if err != nil {
		return err
	}

	if c.jsonOut {
		data, err := json.MarshalIndent(list, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}

	if len(list) == 0 {
		fmt.Fprintln(c.out, "No backups found.")
		return nil
	}
	fmt.Fprintln(c.out, "Perforce backups")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  %-28s  %-12s  %s\n", "ID", "SIZE", "CREATED")
	for _, b := range list {
		fmt.Fprintf(c.out, "  %-28s  %-12d  %s\n", b.ID, b.SizeBytes, b.CreatedAt)
		if b.Description != "" {
			fmt.Fprintf(c.out, "    %s\n", b.Description)
		}
	}
	return nil
}
