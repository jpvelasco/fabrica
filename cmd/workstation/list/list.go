package list

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
)

// WorkstationEntry is the JSON-serialisable view of one workstation.
type WorkstationEntry struct {
	Status     string `json:"status"`
	InstanceID string `json:"instanceId,omitempty"`
	SGID       string `json:"sgId,omitempty"`
}

// ListOutput is the JSON-serialisable result of a list run.
type ListOutput struct {
	Workstations []WorkstationEntry `json:"workstations"`
}

type command struct {
	runtime globals.Runtime
	jsonOut bool
	out     io.Writer

	readState func() (*fabricastate.State, error)
}

// New returns the "workstation list" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provisioned workstations",
		Long:  `List all workstations tracked in local Fabrica state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime: rt,
				jsonOut: opts.JSONOutput,
				out:     out,
			}
			c.readState = c.defaultReadState
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c command) run(_ context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)

	if c.jsonOut {
		return c.printJSON(m)
	}

	c.printText(m)
	return nil
}

func (c command) printText(m *fabricastate.ModuleState) {
	if m == nil {
		fmt.Fprintln(c.out, "No workstations provisioned. Run 'fabrica workstation create' to provision one.")
		return
	}
	fmt.Fprintln(c.out, "Workstations")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Status: %s\n", m.Status)

	if r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
		fmt.Fprintf(c.out, "  Instance ID: %s\n", r.Identifier)
	}
	if r, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
		fmt.Fprintf(c.out, "  Security Group: %s\n", r.Identifier)
	}
}

func (c command) printJSON(m *fabricastate.ModuleState) error {
	out := ListOutput{Workstations: []WorkstationEntry{}}
	if m != nil {
		entry := WorkstationEntry{Status: m.Status}
		if r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
			entry.InstanceID = r.Identifier
		}
		if r, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
			entry.SGID = r.Identifier
		}
		out.Workstations = append(out.Workstations, entry)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling workstations to JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
