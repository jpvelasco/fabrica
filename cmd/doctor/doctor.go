package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/doctorchecks"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment health",
		Long: `Run diagnostic checks against your Fabrica environment.

Checks Go version, Fabrica version, AWS credentials, region, and
the state backend (S3 bucket and DynamoDB lock table).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			backend, _ := rt.Provider.(cloud.StateBackendChecker)
			return command{
				runtime: rt,
				backend: backend,
				json:    opts.JSONOutput,
				out:     out,
			}.run(cmd.Context())
		},
	}
}

type command struct {
	runtime globals.Runtime
	backend cloud.StateBackendChecker
	json    bool
	out     io.Writer
}

func (c command) run(ctx context.Context) error {
	checks := doctorchecks.RunChecks(ctx, c.runtime, c.backend)

	if c.json {
		return c.printJSON(checks)
	}

	fmt.Fprintln(c.out, "Fabrica environment diagnostics")
	fmt.Fprintln(c.out)
	return c.printText(checks)
}

func (c command) printJSON(checks []doctorchecks.DoctorCheck) error {
	b, err := json.MarshalIndent(jsonDiagnostics(checks), "", "  ")
	if err != nil {
		return fmt.Errorf("encoding diagnostics: %w", err)
	}
	fmt.Fprintln(c.out, string(b))
	return nil
}

func (c command) printText(checks []doctorchecks.DoctorCheck) error {
	fails, warns := 0, 0
	for _, d := range checks {
		switch d.Status {
		case "fail":
			fails++
		case "warning":
			warns++
		}
		fmt.Fprintf(c.out, "  %-6s %-26s %s\n", statusSymbol(d.Status), d.Name+":", d.Message)
	}

	fmt.Fprintln(c.out)
	return c.printSummary(fails, warns)
}

func (c command) printSummary(fails, warns int) error {
	if fails > 0 {
		msg := fmt.Sprintf("%d check(s) failed", fails)
		if warns > 0 {
			msg += fmt.Sprintf(", %d warning(s)", warns)
		}
		fmt.Fprintln(c.out, msg)
		return fmt.Errorf("%d diagnostic check(s) failed", fails)
	}
	if warns > 0 {
		fmt.Fprintf(c.out, "All checks passed (%d warning(s)).\n", warns)
		return nil
	}
	fmt.Fprintln(c.out, "All checks passed.")
	return nil
}

func jsonDiagnostics(checks []doctorchecks.DoctorCheck) []map[string]string {
	out := make([]map[string]string, len(checks))
	for i, d := range checks {
		out[i] = map[string]string{
			"name":    d.Name,
			"status":  d.Status,
			"message": d.Message,
		}
	}
	return out
}

func statusSymbol(status string) string {
	switch status {
	case "fail":
		return "[FAIL]"
	case "warning":
		return "[WARN]"
	default:
		return "[OK]"
	}
}

func printDiagnostics(checks []doctorchecks.DoctorCheck) error {
	return command{out: os.Stdout}.printText(checks)
}

func formatDiagnosticSummary(fails, warns int) error {
	return command{out: os.Stdout}.printSummary(fails, warns)
}
