package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// ResolveIdentity returns the account ID and region from the provider.
func ResolveIdentity(ctx context.Context, p cloud.Provider) (string, string, error) {
	account, _, region, err := p.Identity(ctx)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}
	return account, region, nil
}
