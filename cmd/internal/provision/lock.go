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
	"github.com/jpvelasco/fabrica/internal/tags"
)

type lockCtxKey int

const lockHeldKey lockCtxKey = 1

// stateLockLease retains the actually acquired cancellation context even when
// a caller derives a context that suppresses cancellation.
type stateLockLease struct {
	ctx     context.Context
	account string
	region  string
	table   string
}

func (l stateLockLease) matchesTarget(account, region, table string) bool {
	return (account == "" || account == l.account) && (region == "" || region == l.region) && table == l.table
}

func liveStateLock(ctx context.Context) (stateLockLease, bool) {
	lease, ok := ctx.Value(lockHeldKey).(stateLockLease)
	if !ok || ctx.Err() != nil || lease.ctx.Err() != nil {
		return stateLockLease{}, false
	}
	return lease, true
}

// StateLockHeld reports whether ctx carries a live state-lock acquisition.
// Cancellation and release make the acquired context cease to qualify.
func StateLockHeld(ctx context.Context) bool {
	_, live := liveStateLock(ctx)
	return live
}

// ErrStateLocked reports that another fabrica run currently holds the state
// lock. Callers surface it verbatim; the message says what to do.
var ErrStateLocked = errors.New("another fabrica run holds the state lock")

// stateLockTTL is the lease behind the state lock row. It is a variable so
// tests can shorten it; production holds the default 15-minute lease.
var stateLockTTL = fabricastate.DefaultLockTTL

// AcquireStateLock takes the account-level state lock so two concurrent runs
// cannot interleave read-modify-write cycles on remote/local state (road test
// D10). It returns a child context carrying the lock plus a release function
// that is safe to call even after cancellation.
//
// No-op (nil-safe release) when the provider lacks cloud.StateLockManager —
// fake providers in tests/E2E — or when this context already holds the lock,
// so nested orchestration (destroy --all → module teardowns) acquires once.
// AcquireStateLockUnlessDryRun is a no-op on --dry-run so previews stay
// read-only and do not contend with live mutators or leave lock rows.
func AcquireStateLockUnlessDryRun(ctx context.Context, rt globals.Runtime, operation string, dryRun bool) (context.Context, func(), error) {
	if dryRun {
		return ctx, func() {}, nil
	}
	return AcquireStateLock(ctx, rt, operation)
}

func AcquireStateLock(ctx context.Context, rt globals.Runtime, operation string) (context.Context, func(), error) {
	noop := func() {}
	if err := ctx.Err(); err != nil {
		return ctx, noop, err
	}
	if ctx.Value(lockHeldKey) != nil {
		lease, live := liveStateLock(ctx)
		if !live {
			return ctx, noop, fmt.Errorf("state lock is no longer live; acquire from the original live caller context before retrying")
		}
		if rt.Config != nil {
			account := rt.Config.Cloud.AWS.AccountID
			region := rt.Config.Cloud.AWS.Region
			names, err := fabricastate.ConfiguredBackendNames(rt.Config, account)
			if err != nil {
				return ctx, noop, err
			}
			if !lease.matchesTarget(account, region, names.Table) {
				return ctx, noop, fmt.Errorf("state lock belongs to a different account, home region or table; acquire the configured target lock before nesting")
			}
		}
		return ctx, noop, nil
	}
	identity, err := rt.Config.ResolveIdentity()
	if err != nil {
		return ctx, noop, err
	}
	prefix, err := rt.Config.ResolveNamePrefix()
	if err != nil {
		return ctx, noop, err
	}
	custom := identity.ManagedBy() != tags.ManagedByValue
	mgr, ok := rt.Provider.(cloud.StateLockManager)
	if custom && !ok {
		return ctx, noop, fmt.Errorf("custom ownership requires a provider with state locking; run setup with a provider supporting canonical state and account locks")
	}
	if !ok || rt.Config == nil {
		return ctx, noop, nil
	}

	account, region, err := ResolveIdentity(ctx, rt.Provider)
	if err != nil {
		return ctx, noop, fmt.Errorf("resolving account for state lock (%s): %w", operation, err)
	}

	if custom {
		_, ownership, err := fabricastate.InspectBackendOwnership(ctx, rt.Provider, fabricastate.NewSetupPlan(prefix, rt.Config, account, region), identity)
		if err != nil {
			return ctx, noop, err
		}
		if operation != "setup" && (ownership.Bucket.Action != fabricastate.BackendComponentReconcile || ownership.Table.Action != fabricastate.BackendComponentReconcile) {
			return ctx, noop, fmt.Errorf("custom ownership requires an existing compatible state bucket and lock table; run 'fabrica setup' before mutating modules")
		}
	}

	lockID := prefix.StateLockID(account)
	host, _ := os.Hostname()
	holder := fmt.Sprintf("%s pid=%d host=%s", operation, os.Getpid(), shortHostname(host))
	table := fabricastate.ResolveBackendNames(prefix, rt.Config, account).Table

	var token string
	store := fabricastate.NewFuncLockStore(table, stateLockTTL,
		func(ctx context.Context, item map[string]string, condition string, condValues map[string]string) error {
			return mgr.AcquireStateLockRow(ctx, table, item, condition, condValues)
		},
		func(ctx context.Context, key map[string]string, condition string, condValues map[string]string) error {
			return mgr.ReleaseStateLockRow(ctx, table, key["LockID"], token)
		},
	)

	tok, err := store.Acquire(ctx, lockID, holder)
	if err != nil {
		if errors.Is(err, cloud.ErrLockTableMissing) && !custom {
			// Bootstrap ordering: `setup` creates the lock table, so it (and
			// any command on an unbootstrapped account) runs unlocked.
			oplog.WithModule("state-lock").Warn(
				"lock table absent — proceeding WITHOUT locking; run 'fabrica setup' to enable distributed locking")
			return ctx, noop, nil
		}
		if errors.Is(err, cloud.ErrLockHeld) {
			return ctx, noop, fmt.Errorf(
				"%w — wait for the other run to finish, or delete the stale row (LockID %q) from table %q if it is older than %s",
				ErrStateLocked, lockID, table, stateLockTTL)
		}
		return ctx, noop, fmt.Errorf("acquiring state lock (%s): %w", operation, err)
	}
	token = tok

	// Heartbeat keeps AcquiredAt fresh so a live promote (default 45m wait)
	// cannot be taken over after the lease. A crashed process stops renewing
	// and the next run still takes over after it. If renewals keep failing or
	// the lock is taken over, the heartbeat fires onLost and cancels
	// mutateCtx, so the command's in-flight work aborts before the lease
	// lapses.
	renewCtx, stopRenew := context.WithCancel(context.WithoutCancel(ctx))
	mutateCtx, stopMutate := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.Heartbeat(renewCtx, lockID, token, holder, func(err error) {
			oplog.WithModule("state-lock").Error("state lock lost — aborting operation",
				"operation", operation, "error", err)
			stopMutate()
		})
	}()

	release := func() {
		stopRenew()
		stopMutate()
		// Join the heartbeat so no lock machinery (renewals, logging)
		// outlives the release; the wait is bounded by one in-flight
		// renewal, cancelled before the wait starts.
		<-done
		// WithoutCancel: a cancelled command must still free the lock.
		// state.Release swallows takeover races and logs storage failures.
		_ = store.Release(context.WithoutCancel(ctx), lockID, token)
	}
	return context.WithValue(mutateCtx, lockHeldKey, stateLockLease{ctx: mutateCtx, account: account, region: region, table: table}), release, nil
}

// shortHostname trims the domain part off a machine hostname for compact
// lock-holder identities.
func shortHostname(host string) string {
	if i := strings.Index(host, "."); i >= 0 {
		return host[:i]
	}
	return host
}

// BindStateWriter selects an injected seam or binds persistence to the acquired
// mutation context. Call after locking so the writer retains the live lease.
func BindStateWriter(ctx context.Context, rt globals.Runtime, seam func(*fabricastate.State) error) func(*fabricastate.State) error {
	if seam != nil {
		return seam
	}
	return StateWriter(ctx, rt)
}
