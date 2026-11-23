package testutil

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// InstanceProfileProvider extends InspectorProvider with a canned IAM instance
// profile reader so create tests exercise the pre-created profile preflight
// through the provider capability. Profiles maps names to what
// GetInstanceProfile returns; an absent name reports cloud.ErrResourceNotFound.
// GetErr overrides every lookup, for denied or failed reads.
type InstanceProfileProvider struct {
	InspectorProvider

	Profiles map[string]cloud.InstanceProfileInfo
	GetErr   error
	GetCalls []string
}

var _ cloud.InstanceProfileInspector = (*InstanceProfileProvider)(nil)

func (p *InstanceProfileProvider) GetInstanceProfile(_ context.Context, name string) (cloud.InstanceProfileInfo, error) {
	p.GetCalls = append(p.GetCalls, name)
	if p.GetErr != nil {
		return cloud.InstanceProfileInfo{}, p.GetErr
	}
	info, ok := p.Profiles[name]
	if !ok {
		return cloud.InstanceProfileInfo{}, fmt.Errorf("instance profile %s: %w", name, cloud.ErrResourceNotFound)
	}
	return info, nil
}

// OneRoleProfile is a profile with exactly one attached role, the shape create requires.
func OneRoleProfile(name string) map[string]cloud.InstanceProfileInfo {
	return map[string]cloud.InstanceProfileInfo{name: {Name: name, Roles: []string{name + "-role"}}}
}
