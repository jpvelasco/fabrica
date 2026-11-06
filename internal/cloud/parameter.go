package cloud

import (
	"context"
	"errors"
)

// SecureParameter is a Parameter Store SecureString. The value is a secret:
// callers must not place it in Cloud Control desired state, module state, or
// logs. Plan layers stay SDK-free and reach this through ParameterStore.
type SecureParameter struct {
	Name  string
	Value string
	Tags  map[string]string
}

// ParameterStore writes and deletes SecureString parameters. Commands
// type-assert it on the provider. Perforce ADMIN_PASS can adopt this same
// seam later; workstation is the first caller.
type ParameterStore interface {
	// PutSecureParameter creates or overwrites the named SecureString.
	// A parameter left behind by a failed create is replaced, not rejected.
	PutSecureParameter(ctx context.Context, p SecureParameter) error
	// DeleteParameter removes the named parameter. A missing parameter is
	// success so destroy and the create-failure path are idempotent.
	DeleteParameter(ctx context.Context, name string) error
}

// SecureParameterReader reads a SecureString value. It is a separate
// capability from ParameterStore so providers and fakes that only write keep
// compiling; commands type-assert it on the provider. The returned value is a
// secret: callers must not log it or copy it into state.
type SecureParameterReader interface {
	// GetSecureParameter returns the decrypted value of the named parameter.
	// A missing parameter returns an error wrapping ErrParameterNotFound.
	GetSecureParameter(ctx context.Context, name string) (string, error)
}

// ErrParameterNotFound reports that a SecureString does not exist.
var ErrParameterNotFound = errors.New("SSM parameter not found")
