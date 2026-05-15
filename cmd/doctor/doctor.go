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
  State backend reachability`,
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
		checkStateBackend(cmd.Context()),
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

func checkStateBackend(ctx context.Context) diagnostic {
	if globals.Cfg == nil || globals.Cfg.Cloud.Provider == "" {
		return diagnostic{
			"State backend",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	bucket := globals.Cfg.State.Bucket
	table := globals.Cfg.State.Table

	if bucket == "" {
		return diagnostic{
			"State backend",
			"warn",
			"not yet provisioned (run fabrica setup)",
		}
	}

	ok, err := probeState(ctx, globals.Cfg.Cloud.AWS.Region, globals.Cfg.Cloud.AWS.Profile, bucket, table)
	if err != nil {
		return diagnostic{"State backend", "fail", err.Error()}
	}

	if ok {
		return diagnostic{"State backend", "ok", fmt.Sprintf("bucket %s, table %s", bucket, table)}
	}

	return diagnostic{
		"State backend",
		"warn",
		"not yet provisioned (run fabrica setup)",
	}
}

func probeState(ctx context.Context, region, profile, bucket, table string) (bool, error) {
	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(profile))
	}
	cfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return false, fmt.Errorf("loading AWS config: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	s3client := s3.NewFromConfig(cfg)
	_, err = s3client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "404" || apiErr.ErrorCode() == "NoSuchBucket" {
				return false, nil
			}
		}
		return false, fmt.Errorf("checking S3 bucket %s: %w", bucket, err)
	}

	dynClient := dynamodb.NewFromConfig(cfg)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	_, err = dynClient.DescribeTable(ctx2, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
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
