package cloud

import (
	"context"
	"errors"
)

// ErrLockHeld is returned when another run currently holds the state lock.
var ErrLockHeld = errors.New("state lock is held by another fabrica run")

// StateLockManager performs the DynamoDB rows backing state.LockStore.
// Implemented by the AWS provider via the SDK; mechanics only — locking
// policy (conditions, TTL takeover) lives in internal/state.
type StateLockManager interface {
	// AcquireStateLockRow does a conditional put of the lock row. condition
	// uses DynamoDB expression syntax with condValues as its ":name" values.
	// Returns ErrLockHeld when the condition fails (row exists and fresh).
	AcquireStateLockRow(ctx context.Context, table string, item map[string]string, condition string, condValues map[string]string) error
	// ReleaseStateLockRow deletes the lock row conditioned on the caller's
	// token. A failed conditional means another holder took over; it returns
	// ErrLockHeld so callers can log and move on.
	ReleaseStateLockRow(ctx context.Context, table, lockID, token string) error
}
