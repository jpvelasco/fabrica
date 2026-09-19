package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.ImageInspector = (*ec2Service)(nil)

// DescribeImage returns Name/Owner for a single AMI ID.
func (s *ec2Service) DescribeImage(ctx context.Context, imageID string) (fabricac.ImageInfo, error) {
	if err := s.ensureClient(ctx); err != nil {
		return fabricac.ImageInfo{}, err
	}
	out, err := s.client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{imageID},
	})
	if err != nil {
		return fabricac.ImageInfo{}, fmt.Errorf("describing AMI %s: %w", imageID, err)
	}
	if len(out.Images) == 0 {
		return fabricac.ImageInfo{}, fmt.Errorf("AMI %s not found", imageID)
	}
	img := out.Images[0]
	return fabricac.ImageInfo{
		ID:          aws.ToString(img.ImageId),
		Name:        aws.ToString(img.Name),
		Description: aws.ToString(img.Description),
		Owner:       aws.ToString(img.OwnerId),
	}, nil
}
