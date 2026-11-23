package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
)

// VerifyProfile checks a referenced instance profile before any create step
// runs; dry runs call it too. A fabrica-managed profile passes without a call.
// A provider that cannot read profiles fails closed, because an instance must
// not be attached to a profile nobody checked. key is the config setting that
// appears in the error text.
func VerifyProfile(ctx context.Context, p cloud.Provider, ref instanceprofile.Ref, key string) error {
	if !ref.External() {
		return nil
	}
	insp, ok := p.(cloud.InstanceProfileInspector)
	if !ok {
		return fmt.Errorf("cannot verify %s %q: this provider cannot read IAM instance profiles, so the profile is unchecked; refusing to create", key, ref.Name)
	}
	return instanceprofile.Verify(ctx, insp, key, ref.Name)
}

// ProfileLines renders a plan's instance-profile rows. A managed profile shows
// its role and profile rows. A referenced profile shows one row, marked as not
// managed, so the operator sees the role is not created here.
func ProfileLines(ref instanceprofile.Ref, roleLine, profileLine string) []string {
	if ref.External() {
		return []string{profileLine + " (pre-created, not managed)"}
	}
	return []string{roleLine, profileLine}
}
