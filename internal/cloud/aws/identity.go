package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// stsAPIClient is the subset of the STS SDK client surface used here.
type stsAPIClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// seams for testing — overridden in tests, default to the real SDK.
var (
	identityLoadCfg   = loadAWSConfig
	identityNewClient = func(cfg aws.Config) stsAPIClient { return sts.NewFromConfig(cfg) }
)

// callerIdentity resolves the caller's AWS account ID, ARN, and region.
func callerIdentity(ctx context.Context, ac awsConfig) (string, string, string, error) {
	cfg, err := identityLoadCfg(ctx, ac.region, ac.profile)
	if err != nil {
		return "", "", "", fmt.Errorf("loading AWS config: %w", err)
	}

	client := identityNewClient(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", "", "", fmt.Errorf("calling sts:GetCallerIdentity: %w", err)
	}

	account := aws.ToString(out.Account)
	id := aws.ToString(out.Arn)
	return account, id, cfg.Region, nil
}
