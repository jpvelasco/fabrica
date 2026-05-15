package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check environment health",
	Long: `Run diagnostic checks against your Fabrica environment.

Checks Go version, Fabrica version, AWS credentials, region, and
the state backend (S3 bucket and DynamoDB lock table).`,
	RunE: runDoctor,
}

type diagnostic struct {
	name    string
	status  string
	message string
}

type awsProbeConfig struct {
	region  string
	profile string
}

type command struct {
	cfg      *config.Config
	provider cloud.Provider
	json     bool
	out      io.Writer
}

func runDoctor(cmd *cobra.Command, args []string) error {
	rt := globals.Current()
	return command{
		cfg:      rt.Config,
		provider: rt.Provider,
		json:     globals.JSONOutput,
		out:      os.Stdout,
	}.run(cmd.Context())
}

func (c command) run(ctx context.Context) error {
	checks := checker{
		cfg:      c.cfg,
		provider: c.provider,
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
	cfg      *config.Config
	provider cloud.Provider
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
	if r.provider == nil {
		return diagnostic{"AWS credentials", "warning", "no provider configured"}
	}

	_, _, _, err := r.provider.Identity(ctx)
	if err != nil {
		return diagnostic{"AWS credentials", "fail", "could not authenticate — check your credentials and region"}
	}

	return diagnostic{"AWS credentials", "ok", "authenticated"}
}

func (r checker) checkRegion() diagnostic {
	if r.cfg == nil || r.cfg.Cloud.AWS.Region == "" {
		return diagnostic{"Region", "warning", "not set — using " + config.DefaultAWSRegion + " default"}
	}
	return diagnostic{"Region", "ok", r.cfg.Cloud.AWS.Region}
}

func (r checker) checkBucket(ctx context.Context) diagnostic {
	if r.cfg == nil {
		return stateBackendWarning("S3 state bucket")
	}

	bucket := r.cfg.State.Bucket
	if bucket == "" {
		return stateBackendWarning("S3 state bucket")
	}

	ok, err := checkBucketExists(ctx, r.awsProbeConfig(), bucket)
	if err != nil {
		return diagnostic{"S3 state bucket", "fail", "check failed: " + err.Error()}
	}

	if ok {
		return diagnostic{"S3 state bucket", "ok", bucket}
	}

	return diagnostic{"S3 state bucket", "warning", "bucket not found (run fabrica setup)"}
}

func (r checker) checkTable(ctx context.Context) diagnostic {
	if r.cfg == nil {
		return stateBackendWarning("DynamoDB lock table")
	}

	bucket := r.cfg.State.Bucket
	table := r.cfg.State.Table

	// If bucket is not set, setup hasn't run — skip DynamoDB probe
	if bucket == "" {
		return stateBackendWarning("DynamoDB lock table")
	}

	ok, err := checkTableExists(ctx, r.awsProbeConfig(), table)
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

func (r checker) awsProbeConfig() awsProbeConfig {
	return awsProbeConfig{
		region:  r.cfg.Cloud.AWS.Region,
		profile: r.cfg.Cloud.AWS.Profile,
	}
}

func loadAWSDOctorConfig(ctx context.Context, probe awsProbeConfig) (aws.Config, error) {
	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(probe.region),
	}
	if probe.profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(probe.profile))
	}
	return awscfg.LoadDefaultConfig(ctx, opts...)
}

func checkBucketExists(ctx context.Context, probe awsProbeConfig, bucket string) (bool, error) {
	cfg, err := loadAWSDOctorConfig(ctx, probe)
	if err != nil {
		return false, fmt.Errorf("loading AWS config: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := s3.NewFromConfig(cfg)
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "404" || apiErr.ErrorCode() == "NoSuchBucket" {
				return false, nil
			}
		}
		return false, fmt.Errorf("checking S3 bucket %s: %w", bucket, err)
	}

	return true, nil
}

func checkTableExists(ctx context.Context, probe awsProbeConfig, table string) (bool, error) {
	cfg, err := loadAWSDOctorConfig(ctx, probe)
	if err != nil {
		return false, fmt.Errorf("loading AWS config: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := dynamodb.NewFromConfig(cfg)
	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "ResourceNotFoundException" {
				return false, nil
			}
		}
		return false, fmt.Errorf("checking DynamoDB table %s: %w", table, err)
	}

	return true, nil
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
