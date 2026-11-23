package testutil

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// InstanceProfileProvider is an InspectorProvider that reads IAM instance
// profiles from Profiles (absent names report ErrResourceNotFound). GetErr overrides all lookups.
type InstanceProfileProvider struct {
	InspectorProvider

	Profiles map[string]cloud.InstanceProfileInfo
	GetErr   error
}

var _ cloud.InstanceProfileInspector = (*InstanceProfileProvider)(nil)

func (p *InstanceProfileProvider) GetInstanceProfile(_ context.Context, name string) (cloud.InstanceProfileInfo, error) {
	if p.GetErr != nil {
		return cloud.InstanceProfileInfo{}, p.GetErr
	}
	if info, ok := p.Profiles[name]; ok {
		return info, nil
	}
	return cloud.InstanceProfileInfo{}, fmt.Errorf("instance profile %s: %w", name, cloud.ErrResourceNotFound)
}

// OneRoleProfile is a profile with exactly one attached role.
func OneRoleProfile(name string) map[string]cloud.InstanceProfileInfo {
	return map[string]cloud.InstanceProfileInfo{name: {Roles: []string{"role"}}}
}
