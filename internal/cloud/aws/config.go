package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
)

// loadAWSConfig loads an AWS SDK config for the given region and profile.
// If profile is empty, it uses the SDK default credential chain.
func loadAWSConfig(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*awscfg.LoadOptions) error{
		awscfg.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, awscfg.WithSharedConfigProfile(profile))
	}

	cfg, err := awscfg.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("loading AWS config: %w", err)
	}
	return cfg, nil
}
