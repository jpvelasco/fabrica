package aws

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// iamService owns the provider's lazily initialized IAM SDK client. IAM
// instance profiles cannot be tagged through Cloud Control (the create schema
// has no Tags property), so create flows tag them through this client right
// after the profile exists.
type iamService struct {
	awsCfg awsConfig
	client iamAPIClient

	// initMu serializes lazy initialization (concurrent MCP tool calls share
	// this service). Not held during API calls.
	initMu sync.Mutex

	// seams for testing — nil means use real SDK
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) iamAPIClient
}

// iamAPIClient is the subset of the IAM SDK client surface used by iamService.
type iamAPIClient interface {
	TagInstanceProfile(ctx context.Context, params *iam.TagInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error)
}

func (s *iamService) ensureClient(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	if s.client != nil {
		return nil
	}
	loadCfg := s.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, s.awsCfg.region, s.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for IAM service: %w", err)
	}
	newClient := s.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) iamAPIClient {
			return iam.NewFromConfig(cfg)
		}
	}
	s.client = newClient(cfg)
	return nil
}

// TagInstanceProfile applies tags to an IAM instance profile. The API is
// additive (it never removes existing tags) and idempotent, so a re-run of a
// partially failed create re-tags without clobbering.
func (s *iamService) TagInstanceProfile(ctx context.Context, profileName string, tags map[string]string) error {
	if len(tags) == 0 {
		return nil
	}
	if err := s.ensureClient(ctx); err != nil {
		return err
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	iamTags := make([]iamtypes.Tag, 0, len(keys))
	for _, k := range keys {
		iamTags = append(iamTags, iamtypes.Tag{Key: aws.String(k), Value: aws.String(tags[k])})
	}

	if _, err := s.client.TagInstanceProfile(ctx, &iam.TagInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		Tags:                iamTags,
	}); err != nil {
		return fmt.Errorf("tagging IAM instance profile %s: %w", profileName, err)
	}
	return nil
}
