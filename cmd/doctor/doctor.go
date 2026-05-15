package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
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

type diagnostic struct {
	name    string
	status  string
	message string
}

type command struct {
	runtime globals.Runtime
	backend cloud.StateBackendChecker
	json    bool
	out     io.Writer
}

func (c command) run(ctx context.Context) error {
	checks := checker{
		runtime: c.runtime,
		backend: c.backend,
	}.run(ctx)

	if c.json {
		return c.printJSON(checks)
	}

	fmt.Fprintln(c.out, "Fabrica environment diagnostics")
	fmt.Fprintln(c.out)
	return c.printText(checks)
}

func (c command) printJSON(checks []diagnostic) error {
	b, err := json.MarshalIndent(jsonDiagnostics(checks), "", "  ")
	if err != nil {
		return fmt.Errorf("encoding diagnostics: %w", err)
	}
	fmt.Fprintln(c.out, string(b))
	return nil
}

func (c command) printText(checks []diagnostic) error {
	fails, warns := 0, 0
	for _, d := range checks {
		switch d.status {
		case "fail":
			fails++
		case "warning":
			warns++
		}
		fmt.Fprintf(c.out, "  %-6s %-26s %s\n", statusSymbol(d.status), d.name+":", d.message)
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

type checker struct {
	runtime globals.Runtime
	backend cloud.StateBackendChecker
}

func (r checker) run(ctx context.Context) []diagnostic {
	return []diagnostic{
		checkGo(),
		checkVersion(),
		r.checkCreds(ctx),
		r.checkRegion(),
		r.checkBucket(ctx),
		r.checkTable(ctx),
	}
}

func jsonDiagnostics(checks []diagnostic) []map[string]string {
	out := make([]map[string]string, len(checks))
	for i, d := range checks {
		out[i] = map[string]string{
			"name":    d.name,
			"status":  d.status,
			"message": d.message,
		}
	}
	return out
}

func checkGo() diagnostic {
	msg := runtime.Version()
	return diagnostic{"Go version", "ok", msg}
}

func checkVersion() diagnostic {
	msg := fabricav.Version
	if fabricav.Commit != "" {
		msg += " (commit " + fabricav.Commit + ")"
	}
	return diagnostic{"Fabrica version", "ok", msg}
}

func (r checker) checkCreds(ctx context.Context) diagnostic {
	if r.runtime.Provider == nil {
		return diagnostic{"AWS credentials", "warning", "no provider configured"}
	}

	_, _, _, err := r.runtime.Provider.Identity(ctx)
	if err != nil {
		return diagnostic{"AWS credentials", "fail", "could not authenticate — check your credentials and region"}
	}

	return diagnostic{"AWS credentials", "ok", "authenticated"}
}

func (r checker) checkRegion() diagnostic {
	if r.runtime.Config == nil || r.runtime.Config.Cloud.AWS.Region == "" {
		return diagnostic{"Region", "warning", "not set — using " + config.DefaultAWSRegion + " default"}
	}
	return diagnostic{"Region", "ok", r.runtime.Config.Cloud.AWS.Region}
}

func (r checker) checkBucket(ctx context.Context) diagnostic {
	if r.runtime.Config == nil {
		return stateBackendWarning("S3 state bucket")
	}

	bucket := r.runtime.Config.State.Bucket
	if bucket == "" {
		return stateBackendWarning("S3 state bucket")
	}

	if r.backend == nil {
		return diagnostic{"S3 state bucket", "warning", "state backend checker unavailable for provider"}
	}

	ok, err := r.backend.StateBucketExists(ctx, bucket)
	if err != nil {
		return diagnostic{"S3 state bucket", "fail", "check failed: " + err.Error()}
	}

	if ok {
		return diagnostic{"S3 state bucket", "ok", bucket}
	}

	return diagnostic{"S3 state bucket", "warning", "bucket not found (run fabrica setup)"}
}

func (r checker) checkTable(ctx context.Context) diagnostic {
	if r.runtime.Config == nil {
		return stateBackendWarning("DynamoDB lock table")
	}

	bucket := r.runtime.Config.State.Bucket
	table := r.runtime.Config.State.Table

	// If bucket is not set, setup hasn't run — skip DynamoDB probe
	if bucket == "" {
		return stateBackendWarning("DynamoDB lock table")
	}

	if r.backend == nil {
		return diagnostic{"DynamoDB lock table", "warning", "state backend checker unavailable for provider"}
	}

	ok, err := r.backend.StateLockTableExists(ctx, table)
	if err != nil {
		return diagnostic{"DynamoDB lock table", "fail", "check failed: " + err.Error()}
	}

	if ok {
		return diagnostic{"DynamoDB lock table", "ok", table}
	}

	return diagnostic{"DynamoDB lock table", "warning", "table not found (run fabrica setup)"}
}

func stateBackendWarning(name string) diagnostic {
	return diagnostic{name, "warning", "not yet provisioned (run fabrica setup)"}
}

func printDiagnostics(checks []diagnostic) error {
	return command{out: os.Stdout}.printText(checks)
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

func formatDiagnosticSummary(fails, warns int) error {
	return command{out: os.Stdout}.printSummary(fails, warns)
}
