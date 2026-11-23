// Package instanceprofile is the shared contract for a module's optional
// pre-created instance profile (<module>.instanceProfile). The platform team owns
// the profile and its role; Fabrica references them and never creates or deletes
// them.
package instanceprofile

import (
	"context"
	"errors"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// PropertyExternal marks a module resource recorded as pre-created.
const PropertyExternal = "external"

// Ref is a module's resolved instance-profile setting. The zero Ref means
// Fabrica creates the role and profile.
type Ref struct {
	Name string
}

// Resolve validates a configured name or instance-profile ARN.
func Resolve(value string) (Ref, error) {
	name, err := config.InstanceProfileName(value)
	return Ref{Name: name}, err
}

// External reports whether the profile is referenced rather than managed.
func (r Ref) External() bool { return r.Name != "" }

// RecordedTypes lists the IAM types a module records: role and profile when
// managed, only the profile when referenced.
func (r Ref) RecordedTypes() []string {
	if r.External() {
		return []string{cloud.TypeAWSIAMInstanceProfile}
	}
	return []string{cloud.TypeAWSIAMRole, cloud.TypeAWSIAMInstanceProfile}
}

// IsExternal reports whether a recorded resource is a referenced profile.
func IsExternal(r fabricastate.ModuleResource) bool {
	return r.Properties[PropertyExternal] == "true"
}

// Verify confirms the profile exists with exactly one role attached. key is the
// config setting, named in each error so the operator knows what to fix.
func Verify(ctx context.Context, insp cloud.InstanceProfileInspector, key, name string) error {
	info, err := insp.GetInstanceProfile(ctx, name)
	switch {
	case errors.Is(err, cloud.ErrResourceNotFound):
		return fmt.Errorf("%s %q: no instance profile with that name exists. Create it with the module's role policies (docs/guardrailed-accounts.md, Pre-created instance profiles), or set %s to an existing profile", key, name, key)
	case errors.Is(err, cloud.ErrInspectionDenied):
		return fmt.Errorf("%s %q: the caller lacks iam:GetInstanceProfile on this profile. Set %s to a profile this caller can read, or have the platform team grant iam:GetInstanceProfile: %w", key, name, key, err)
	case err != nil:
		return fmt.Errorf("%s %q: reading the instance profile failed: %w. Check AWS credentials and retry", key, name, err)
	}
	if n := len(info.Roles); n != 1 {
		return fmt.Errorf("%s %q has %d roles attached; it needs exactly one. Attach one role to the profile, or set %s to a profile that has one", key, name, n, key)
	}
	return nil
}
