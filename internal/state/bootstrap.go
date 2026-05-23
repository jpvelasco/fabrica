package state

import (
	"context"
	"errors"
	"fmt"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// ErrBootstrapNotImplemented is returned by Bootstrap until the real AWS SDK
// calls are wired. Callers should detect it with errors.Is and print a
// user-facing explanation rather than treating it as a hard failure.
var ErrBootstrapNotImplemented = errors.New(
	"state backend bootstrap is not yet automated; create the S3 bucket and DynamoDB table manually",
)

// BootstrapResult describes the outcome of one bootstrapping step.
type BootstrapResult struct {
	Name    string
	Existed bool
}

func (r BootstrapResult) String() string {
	if r.Existed {
		return fmt.Sprintf("  %s already exists — skipping", r.Name)
	}
	return fmt.Sprintf("  created %s", r.Name)
}

// Bootstrap creates the S3 state bucket and DynamoDB lock table for this account.
// It currently returns ErrBootstrapNotImplemented — the real AWS calls are not yet
// wired. The identity check runs first so credential problems surface immediately.
func Bootstrap(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]BootstrapResult, error) {
	_, _, _, err := provider.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving identity: %w", err)
	}
	return nil, ErrBootstrapNotImplemented
}
