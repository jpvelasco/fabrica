package cloud

import (
	"context"
	"errors"
)

// ErrStateBucketNotEmpty is returned when a state bucket cannot be deleted
// because it still contains objects.
var ErrStateBucketNotEmpty = errors.New("state bucket is not empty")

// StateBackendChecker verifies the storage primitives used by Fabrica state.
type StateBackendChecker interface {
	StateBucketExists(ctx context.Context, bucket string) (bool, error)
	StateLockTableExists(ctx context.Context, table string) (bool, error)
}

// StateBackendDeleteResult describes one idempotent state-backend deletion.
type StateBackendDeleteResult struct {
	Identifier string
	Deleted    bool
	Missing    bool
}

// StateBackendDestroyer deletes the storage primitives used by Fabrica state.
type StateBackendDestroyer interface {
	DeleteStateBucket(ctx context.Context, bucket string) (StateBackendDeleteResult, error)
	DeleteStateLockTable(ctx context.Context, table string) (StateBackendDeleteResult, error)
}
