package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostic checks",
	Long: `Performs diagnostics on the Fabrica environment:

  Go version
  AWS credentials
  Region
  Fabrica version
  State backend (S3 bucket + DynamoDB lock table)`,
	RunE: runDoctor,
}

type diagnostic struct {
	name    string
	status  string
	message string
}

func runDoctor(cmd *cobra.Command, args []string) error {
	fmt.Println("Running diagnostics...")
	fmt.Println()

	var checks []diagnostic
	checks = append(checks,
		checkGo(),
		checkVersion(),
		checkCreds(cmd.Context()),
		checkRegion(),
		checkBucket(cmd.Context()),
		checkTable(cmd.Context()),
	)

	if globals.JSONOutput {
		var out = make([]any, len(checks))
		for i, d := range checks {
			out[i] = map[string]string{
				"name":    d.name,
				"status":  d.status,
				"message": d.message,
			}
		}
		b, _ := json.Marshal(out)
		fmt.Println(string(b))
	}

	return printDiagnostics(checks)
}

func checkGo() diagnostic {
	msg := fmt.Sprintf("Go %s", runtime.Version())
	return diagnostic{"Go version", "ok", msg}
}

func checkVersion() diagnostic {
	msg := fmt.Sprintf("Fabrica %s (commit %s)", fabricav.Version, fabricav.Commit)
	return diagnostic{"Fabrica version", "ok", msg}
}

func checkCreds(ctx context.Context) diagnostic {
	if globals.Provider == nil {
		return diagnostic{"AWS credentials", "warn", "no provider configured"}
	}

	_, _, _, err := globals.Provider.Identity(ctx)
	if err != nil {
		return diagnostic{"AWS credentials", "fail", err.Error()}
	}

	return diagnostic{"AWS credentials", "ok", "authenticated"}
}

func checkRegion() diagnostic {
	if globals.Cfg == nil || globals.Cfg.Cloud.AWS.Region == "" {
		return diagnostic{"Region", "warn", "not set in config (default: us-east-1)"}
	}
	return diagnostic{"Region", "ok", globals.Cfg.Cloud.AWS.Region}
}

func checkBucket(ctx context.Context) diagnostic {
	if globals.Cfg == nil {
		return diagnostic{
			"S3 state bucket",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	bucket := globals.Cfg.State.Bucket
	if bucket == "" {
		return diagnostic{
			"S3 state bucket",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	region := globals.Cfg.Cloud.AWS.Region
	profile := globals.Cfg.Cloud.AWS.Profile

	ok, err := checkBucketExists(ctx, region, profile, bucket)
	if err != nil {
		return diagnostic{"S3 state bucket", "fail", err.Error()}
	}

	if ok {
		return diagnostic{"S3 state bucket", "ok", bucket}
	}

	return diagnostic{
		"S3 state bucket",
		"warn",
		"not found (run fabrica setup)",
	}
}

func checkTable(ctx context.Context) diagnostic {
	if globals.Cfg == nil {
		return diagnostic{
			"DynamoDB lock table",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	bucket := globals.Cfg.State.Bucket
	table := globals.Cfg.State.Table

	// If bucket is not set, setup hasn't run — don't probe DynamoDB
	if bucket == "" {
		return diagnostic{
			"DynamoDB lock table",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	region := globals.Cfg.Cloud.AWS.Region
	profile := globals.Cfg.Cloud.AWS.Profile

	ok, err := checkTableExists(ctx, region, profile, table)
	if err != nil {
		return diagnostic{"DynamoDB lock table", "fail", err.Error()}
	}

	if ok {
		return diagnostic{"DynamoDB lock table", "ok", table}
	}

	return diagnostic{
		"DynamoDB lock table",
		"warn",
		"not found (run fabrica setup)",
	}
}

func loadAWSDOctorConfig(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(profile))
	}
	return awscfg.LoadDefaultConfig(ctx, opts...)
}

func checkBucketExists(ctx context.Context, region, profile, bucket string) (bool, error) {
	cfg, err := loadAWSDOctorConfig(ctx, region, profile)
	if err != nil {
		return false, fmt.Errorf("loading AWS config: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := s3.NewFromConfig(cfg)
	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	_, err = client.HeadBucket(ctx2, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
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

func checkTableExists(ctx context.Context, region, profile, table string) (bool, error) {
	cfg, err := loadAWSDOctorConfig(ctx, region, profile)
	if err != nil {
		return false, fmt.Errorf("loading AWS config: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := dynamodb.NewFromConfig(cfg)
	ctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()

	_, err = client.DescribeTable(ctx2, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
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
	fails, warns := 0, 0
	for _, d := range checks {
		switch d.status {
		case "fail":
			fails++
		case "warn":
			warns++
		}
		marker := diagnosticMarker(d.status)
		fmt.Printf("  %s %-30s %s\n", marker, d.name, d.message)
	}
	fmt.Println()
	return formatDiagnosticSummary(fails, warns)
}

func diagnosticMarker(status string) string {
	switch status {
	case "fail":
		return "[FAIL]"
	case "warn":
		return "[WARN]"
	default:
		return "[OK]  "
	}
}

func formatDiagnosticSummary(fails, warns int) error {
	if fails > 0 {
		fmt.Printf("%d issue(s) found", fails)
		if warns > 0 {
			fmt.Printf(", %d warning(s)", warns)
		}
		fmt.Println()
		return fmt.Errorf("%d diagnostic check(s) failed", fails)
	}
	if warns > 0 {
		fmt.Printf("No issues found (%d warning(s)).\n", warns)
	} else {
		fmt.Println("No issues found.")
	}
	return nil
}
