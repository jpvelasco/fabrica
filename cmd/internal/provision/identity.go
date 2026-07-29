package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// ResolveIdentity returns the account ID and region from the provider.
func ResolveIdentity(ctx context.Context, p cloud.Provider) (string, string, error) {
	if p == nil {
		return "", "", fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}
	account, _, region, err := p.Identity(ctx)
	if err != nil {
		return "", "", fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}
	return account, region, nil
}
