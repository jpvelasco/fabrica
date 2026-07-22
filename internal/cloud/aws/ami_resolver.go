package aws

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.AMIResolver = (*amiResolver)(nil)

// amiResolver resolves base AMIs via ec2:DescribeImages.
type amiResolver struct {
	awsCfg awsConfig
	client ec2APIClient

	// seams for testing — nil means use real SDK
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ec2APIClient
}

func newAMIResolver(cfg awsConfig) *amiResolver {
	return &amiResolver{awsCfg: cfg}
}

func (r *amiResolver) ensureClient(ctx context.Context) error {
	if r.client != nil {
		return nil
	}
	loadCfg := r.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, r.awsCfg.region, r.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for AMI resolver: %w", err)
	}
	newClient := r.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ec2APIClient {
			return ec2.NewFromConfig(cfg)
		}
	}
	r.client = newClient(cfg)
	return nil
}

// ResolveUbuntuAMI returns the latest Ubuntu 22.04 (jammy) HVM AMI for the
// given region. It queries the canonical Canonical owner (099720109477) for
// ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server snapshots, selects the
// most recent by creation date, and returns its AMI ID.
func (r *amiResolver) ResolveUbuntuAMI(ctx context.Context, region string) (string, error) {
	if err := r.ensureClient(ctx); err != nil {
		return "", err
	}

	// Canonical's official Ubuntu owner ID.
	const canonicalOwnerID = "099720109477"

	out, err := r.client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{canonicalOwnerID},
		Filters: []types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{"ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"},
			},
			{Name: aws.String("state"), Values: []string{"available"}},
			{Name: aws.String("virtualization-type"), Values: []string{"hvm"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describing Ubuntu AMIs: %w", err)
	}

	if len(out.Images) == 0 {
		return "", fmt.Errorf("no Ubuntu 22.04 AMI found in region %s", region)
	}

	// Sort by creation date descending — the most recent image first.
	// CreationDate is an ISO 8601 string in the SDK v2, so lexicographic
	// comparison works correctly.
	images := out.Images
	sort.Slice(images, func(i, j int) bool {
		return aws.ToString(images[i].CreationDate) > aws.ToString(images[j].CreationDate)
	})

	return aws.ToString(images[0].ImageId), nil
}
