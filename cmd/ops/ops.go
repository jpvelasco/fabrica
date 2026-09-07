// Package ops implements `fabrica ops`: optional observability export hooks.
// V1 is offline — it writes local dashboard/log/alarm documents operators
// import into CloudWatch or Grafana. No AWS resources are created.
package ops

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricaops "github.com/jpvelasco/fabrica/internal/ops"
	"github.com/spf13/cobra"
)

const defaultOutput = "ops-export.json"

// New returns the "ops" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ops",
		Short: "Optional observability export hooks",
		Long: `Optional observability for provisioned modules.

V1 writes local dashboard, log-group, and alarm hooks for Perforce, Horde,
DDC, CI, and Deploy. Enable with ops.enabled in fabrica.yaml. Fabrica does
not provision CloudWatch resources — import the export, or leave ops disabled.

Available operations:
  export   Write the local observability hook document`,
	}
	cmd.AddCommand(newExport(runtimeSource, optionsSource, out))
	return cmd
}

type exportCommand struct {
	rt      globals.Runtime
	dryRun  bool
	jsonOut bool
	output  string
	out     io.Writer

	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
}

func newExport(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write local observability hooks for enabled modules",
		Long: `Write a local JSON document of log groups, alarms, and metric names
for the modules listed in ops.modules (default: perforce, horde, ddc, ci, deploy).

Requires ops.enabled: true. No AWS calls. --dry-run prints the document
without writing a file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := exportCommand{
				rt:        rt,
				dryRun:    opts.DryRun,
				jsonOut:   opts.JSONOutput,
				output:    output,
				out:       out,
				writeFile: os.WriteFile,
				mkdirAll:  os.MkdirAll,
			}
			return c.run()
		},
	}
	cmd.Flags().StringVar(&output, "output", defaultOutput, "Output file path")
	return cmd
}

func (c exportCommand) run() error {
	if !c.rt.Config.Ops.Enabled {
		return fmt.Errorf("ops is disabled — set ops.enabled: true in fabrica.yaml to export hooks")
	}
	doc, err := fabricaops.BuildExport(c.rt.Config.Ops)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding ops export: %w", err)
	}
	payload = append(payload, '\n')
	if c.dryRun || c.jsonOut {
		fmt.Fprint(c.out, string(payload))
		return nil
	}
	path := c.output
	if path == "" {
		path = defaultOutput
	}
	path = filepath.Clean(path)
	if !relativeExportPath(path) {
		return fmt.Errorf("ops export path %q must be a relative path under the current directory", path)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := c.mkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating ops export dir %s: %w", dir, err)
		}
	}
	if err := c.writeFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(c.out, "Wrote %s (%d modules). Import into CloudWatch or Grafana — Fabrica does not provision these resources.\n", path, len(doc.Modules))
	return nil
}

func relativeExportPath(path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "..") {
		return false
	}
	// Unix-style absolute paths stay absolute on Windows (filepath.IsAbs is false).
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return false
	}
	return true
}
