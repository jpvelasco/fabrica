package provision

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type lockCtxKey int

const lockHeldKey lockCtxKey = 1

// ErrStateLocked reports that another fabrica run currently holds the state
// lock. Callers surface it verbatim; the message says what to do.
var ErrStateLocked = errors.New("another fabrica run holds the state lock")

// AcquireStateLock takes the account-level state lock so two concurrent runs
// cannot interleave read-modify-write cycles on remote/local state (road test
// D10). It returns a child context carrying the lock plus a release function
// that is safe to call even after cancellation.
//
// No-op (nil-safe release) when the provider lacks cloud.StateLockManager —
// fake providers in tests/E2E — or when this context already holds the lock,
// so nested orchestration (destroy --all → module teardowns) acquires once.
func AcquireStateLock(ctx context.Context, rt globals.Runtime, operation string) (context.Context, func(), error) {
	noop := func() {}
	if ctx.Value(lockHeldKey) != nil {
		return ctx, noop, nil
	}
	mgr, ok := rt.Provider.(cloud.StateLockManager)
	if !ok || rt.Config == nil {
		return ctx, noop, nil
	}

	account, _, err := ResolveIdentity(ctx, rt.Provider)
	if err != nil {
		return ctx, noop, fmt.Errorf("resolving account for state lock (%s): %w", operation, err)
	}

	lockID := "fabrica-state/" + account
	host, _ := os.Hostname()
	holder := fmt.Sprintf("%s pid=%d host=%s", operation, os.Getpid(), shortHostname(host))
	table := rt.Config.State.Table

	var token string
	store := fabricastate.NewFuncLockStore(table, fabricastate.DefaultLockTTL,
		func(ctx context.Context, item map[string]string, condition string, condValues map[string]string) error {
			return mgr.AcquireStateLockRow(ctx, table, item, condition, condValues)
		},
		func(ctx context.Context, key map[string]string, condition string, condValues map[string]string) error {
			return mgr.ReleaseStateLockRow(ctx, table, key["LockID"], token)
		},
	)

	tok, err := store.Acquire(ctx, lockID, holder)
	if err != nil {
		if errors.Is(err, cloud.ErrLockTableMissing) {
			// Bootstrap ordering: `setup` creates the lock table, so it (and
			// any command on an unbootstrapped account) runs unlocked.
			oplog.WithModule("state-lock").Warn(
				"lock table absent — proceeding WITHOUT locking; run 'fabrica setup' to enable distributed locking")
			return ctx, noop, nil
		}
		if errors.Is(err, cloud.ErrLockHeld) {
			return ctx, noop, fmt.Errorf(
				"%w — wait for the other run to finish, or delete the stale row (LockID %q) from table %q if it is older than %s",
				ErrStateLocked, lockID, table, fabricastate.DefaultLockTTL)
		}
		return ctx, noop, fmt.Errorf("acquiring state lock (%s): %w", operation, err)
	}
	token = tok

	release := func() {
		// WithoutCancel: a cancelled command must still free the lock.
		// state.Release swallows takeover races and logs storage failures.
		_ = store.Release(context.WithoutCancel(ctx), lockID, token)
	}
	return context.WithValue(ctx, lockHeldKey, holder), release, nil
}

// shortHostname trims the domain part off a machine hostname for compact
// lock-holder identities.
func shortHostname(host string) string {
	if i := strings.Index(host, "."); i >= 0 {
		return host[:i]
	}
	return host
}
