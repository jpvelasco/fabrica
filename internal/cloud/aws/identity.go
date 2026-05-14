package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// callerIdentity resolves the caller's AWS account ID, ARN, and region.
func callerIdentity(ctx context.Context, ac awsConfig) (string, string, string, error) {
	cfg, err := loadAWSConfig(ctx, ac.region, ac.profile)
	if err != nil {
		return "", "", "", fmt.Errorf("loading AWS config: %w", err)
	}

	client := sts.NewFromConfig(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", "", "", fmt.Errorf("calling sts:GetCallerIdentity: %w", err)
	}

	account := *out.Account
	id := aws.ToString(out.Arn)
	return account, id, cfg.Region, nil
}
