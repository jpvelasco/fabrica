package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// ParameterPut returns the provider's SecureString write seam, or nil when the
// provider does not implement cloud.ParameterStore. A create that needs a
// SecureString refuses to run without it rather than launching an instance
// that cannot read its secret.
func ParameterPut(p cloud.Provider) func(context.Context, cloud.SecureParameter) error {
	store, ok := p.(cloud.ParameterStore)
	if !ok {
		return nil
	}
	return store.PutSecureParameter
}

// ParameterDelete returns the provider's SecureString delete seam, or nil when
// the provider does not implement cloud.ParameterStore.
func ParameterDelete(p cloud.Provider) func(context.Context, string) error {
	store, ok := p.(cloud.ParameterStore)
	if !ok {
		return nil
	}
	return store.DeleteParameter
}

// AbandonSecureParameter deletes a SecureString left behind by a create that
// failed before its instance existed, so the secret does not outlive the
// attempt. It returns cause unchanged when the delete succeeds. When the delete
// fails, or there is no delete seam, the returned error names the surviving
// parameter so the operator can remove it by hand.
func AbandonSecureParameter(ctx context.Context, del func(context.Context, string) error, name string, cause error) error {
	if del == nil {
		return fmt.Errorf("%w (SSM parameter %s was not deleted: no parameter store)", cause, name)
	}
	if err := del(ctx, name); err != nil {
		return fmt.Errorf("%w (also failed to delete SSM parameter %s: %v)", cause, name, err)
	}
	return cause
}
