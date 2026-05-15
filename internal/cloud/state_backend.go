package cloud

import "context"

// StateBackendChecker verifies the storage primitives used by Fabrica state.
type StateBackendChecker interface {
	StateBucketExists(ctx context.Context, bucket string) (bool, error)
	StateLockTableExists(ctx context.Context, table string) (bool, error)
}
