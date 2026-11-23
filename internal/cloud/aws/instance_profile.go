package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.InstanceProfileInspector = (*awsProvider)(nil)

// GetInstanceProfile reads a pre-created instance profile and the names of the
// roles attached to it. A missing profile wraps cloud.ErrResourceNotFound; a
// denied read wraps cloud.ErrInspectionDenied, naming iam:GetInstanceProfile.
func (s *iamService) GetInstanceProfile(ctx context.Context, name string) (fabricac.InstanceProfileInfo, error) {
	if err := s.ensureClient(ctx); err != nil {
		return fabricac.InstanceProfileInfo{}, err
	}
	out, err := s.client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(name)})
	if err != nil {
		return fabricac.InstanceProfileInfo{}, instanceProfileError(name, err)
	}
	info := fabricac.InstanceProfileInfo{Name: aws.ToString(out.InstanceProfile.InstanceProfileName)}
	for _, role := range out.InstanceProfile.Roles {
		info.Roles = append(info.Roles, aws.ToString(role.RoleName))
	}
	return info, nil
}

// instanceProfileError classifies a GetInstanceProfile failure by its AWS error
// code so preflight can name the problem.
func instanceProfileError(name string, err error) error {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchEntity":
			return fmt.Errorf("reading instance profile %s: %w: %w", name, fabricac.ErrResourceNotFound, err)
		case "AccessDenied", "UnauthorizedOperation":
			return fmt.Errorf("reading instance profile %s needs iam:GetInstanceProfile: %w: %w", name, fabricac.ErrInspectionDenied, err)
		}
	}
	return fmt.Errorf("reading instance profile %s: %w", name, err)
}

// GetInstanceProfile delegates to the IAM service so *awsProvider satisfies
// cloud.InstanceProfileInspector; create preflight asserts it on the provider.
func (p *awsProvider) GetInstanceProfile(ctx context.Context, name string) (fabricac.InstanceProfileInfo, error) {
	return p.iam.GetInstanceProfile(ctx, name)
}
