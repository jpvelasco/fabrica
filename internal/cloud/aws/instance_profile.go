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

// GetInstanceProfile reads a pre-created profile and the names of its roles.
// NoSuchEntity wraps cloud.ErrResourceNotFound; AccessDenied wraps
// cloud.ErrInspectionDenied and names iam:GetInstanceProfile.
func (s *iamService) GetInstanceProfile(ctx context.Context, name string) (fabricac.InstanceProfileInfo, error) {
	if err := s.ensureClient(ctx); err != nil {
		return fabricac.InstanceProfileInfo{}, err
	}
	out, err := s.client.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: aws.String(name)})
	if err != nil {
		var apiErr interface{ ErrorCode() string }
		switch {
		case errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchEntity":
			return fabricac.InstanceProfileInfo{}, fmt.Errorf("reading instance profile %s: %w: %w", name, fabricac.ErrResourceNotFound, err)
		case errors.As(err, &apiErr) && (apiErr.ErrorCode() == "AccessDenied" || apiErr.ErrorCode() == "UnauthorizedOperation"):
			return fabricac.InstanceProfileInfo{}, fmt.Errorf("reading instance profile %s needs iam:GetInstanceProfile: %w: %w", name, fabricac.ErrInspectionDenied, err)
		}
		return fabricac.InstanceProfileInfo{}, fmt.Errorf("reading instance profile %s: %w", name, err)
	}
	info := fabricac.InstanceProfileInfo{Name: aws.ToString(out.InstanceProfile.InstanceProfileName)}
	for _, role := range out.InstanceProfile.Roles {
		info.Roles = append(info.Roles, aws.ToString(role.RoleName))
	}
	return info, nil
}

// GetInstanceProfile delegates to the IAM service so *awsProvider satisfies
// cloud.InstanceProfileInspector.
func (p *awsProvider) GetInstanceProfile(ctx context.Context, name string) (fabricac.InstanceProfileInfo, error) {
	return p.iam.GetInstanceProfile(ctx, name)
}
