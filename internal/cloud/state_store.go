package cloud

import (
	"context"
	"errors"
)

// ErrStateDocumentMissing reports that the remote state document does not
// exist — either it has not been written yet or the state bucket has not been
// bootstrapped. Readers fall back to the local state cache.
var ErrStateDocumentMissing = errors.New("remote state document not found")

// ErrStateBackendAbsent reports that the state bucket does not exist, so no
// document can be written into it. Writers degrade to local-only mode with a
// warning — the same bootstrap ordering the lock table follows.
var ErrStateBackendAbsent = errors.New("state bucket not found")

// StateStore persists the canonical provisioning-state document in the
// account's state bucket. The layout (bucket, object key) is chosen by the
// caller. GetStateDocument maps a missing object or a missing bucket to
// ErrStateDocumentMissing; PutStateDocument maps a missing bucket to
// ErrStateBackendAbsent. Every other storage error is propagated so callers
// can surface it.
type StateStore interface {
	GetStateDocument(ctx context.Context, bucket, key string) ([]byte, error)
	PutStateDocument(ctx context.Context, bucket, key string, data []byte) error
}
