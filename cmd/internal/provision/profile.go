package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
)

// VerifyProfile checks a referenced instance profile before any create runs,
// dry runs included. A managed profile passes without a call. A provider that
// cannot read profiles fails closed. key is the config setting named in errors.
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

// ProfileLines renders a plan's profile rows. A referenced profile is one row
// marked as not managed; a managed one shows its role and profile rows.
func ProfileLines(ref instanceprofile.Ref, roleLine, profileLine string) []string {
	if ref.External() {
		return []string{profileLine + " (pre-created, not managed)"}
	}
	return []string{roleLine, profileLine}
}
