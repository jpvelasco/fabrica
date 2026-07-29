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

var _ fabricac.AMIResolver = (*ec2Service)(nil)

// ResolveUbuntuAMI returns the latest Ubuntu 22.04 (jammy) HVM AMI for the
// given region. It queries the Canonical owner (099720109477) for
// ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server snapshots, selects the
// most recent by creation date, and returns its AMI ID.
func (s *ec2Service) ResolveUbuntuAMI(ctx context.Context, region string) (string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return "", err
	}

	// Canonical's official Ubuntu owner ID.
	const canonicalOwnerID = "099720109477"

	out, err := s.client.DescribeImages(ctx, &ec2.DescribeImagesInput{
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
