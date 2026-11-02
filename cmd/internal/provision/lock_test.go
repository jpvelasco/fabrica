package provision

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// fakeLockManager records lock calls for bridge tests. Renewals are
// recognized by the RenewCondition expression and routed to renewErr.
// The counters are atomic: the heartbeat goroutine renews concurrently
// with the test goroutine that asserts on them.
type fakeLockManager struct {
	acquires   atomic.Int32
	renews     atomic.Int32
	releases   atomic.Int32
	held       bool
	releaseErr error
	acquireErr error
	renewErr   error
}

func (f *fakeLockManager) AcquireStateLockRow(_ context.Context, _ string, _ map[string]string, condition string, _ map[string]string) error {
	if condition == fabricastate.RenewCondition {
		f.renews.Add(1)
		return f.renewErr
	}
	f.acquires.Add(1)
	if f.acquireErr != nil {
		return f.acquireErr
	}
	if f.held {
		return cloud.ErrLockHeld
	}
	return nil
}

func (f *fakeLockManager) ReleaseStateLockRow(_ context.Context, _, _, _ string) error {
	f.releases.Add(1)
	return f.releaseErr
}

type lockingProvider struct {
	*testutil.TestProvider
	*fakeLockManager
}

func testRuntimeWithLocker(l *fakeLockManager) globals.Runtime {
	return globals.Runtime{Config: config.Defaults(), Provider: &lockingProvider{TestProvider: &testutil.TestProvider{}, fakeLockManager: l}}
}

func TestAcquireStateLockNoCapabilityIsNoop(t *testing.T) {
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.TestProvider{}}
	ctx, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("expected silent no-op: %v", err)
	}
	if ctx.Value(lockHeldKey) != nil {
		t.Error("sentinel must not be set for capability-less providers")
	}
	release()
}

func TestAcquireStateLockAcquiresAndReleases(t *testing.T) {
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)
	ctx, release, err := AcquireStateLock(context.Background(), rt, "perforce destroy")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if ctx.Value(lockHeldKey) == nil {
		t.Error("lock sentinel missing from returned context")
	}
	if locker.acquires.Load() != 1 {
		t.Errorf("acquires = %d, want 1", locker.acquires.Load())
	}
	release()
	if locker.releases.Load() != 1 {
		t.Errorf("releases = %d, want 1", locker.releases.Load())
	}
}

func TestAcquireStateLockNestedSkips(t *testing.T) {
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)
	ctx, release, err := AcquireStateLock(context.Background(), rt, "outer")
	if err != nil {
		t.Fatalf("outer acquire: %v", err)
	}
	defer release()

	_, nestedRelease, err := AcquireStateLock(ctx, rt, "inner")
	if err != nil {
		t.Fatalf("nested acquire must inherit the outer lock: %v", err)
	}
	nestedRelease()
	if locker.acquires.Load() != 1 {
		t.Errorf("acquires = %d, want 1 (nested must not re-acquire)", locker.acquires.Load())
	}
	if locker.releases.Load() != 0 {
		t.Errorf("releases = %d before outer release; nested release must be a no-op", locker.releases.Load())
	}
}

func TestAcquireStateLockHeldWrapsActionableError(t *testing.T) {
	locker := &fakeLockManager{held: true}
	rt := testRuntimeWithLocker(locker)
	_, _, err := AcquireStateLock(context.Background(), rt, "horde destroy")
	if err == nil {
		t.Fatal("expected held-lock error")
	}
	if !errors.Is(err, ErrStateLocked) {
		t.Fatalf("err = %v, want ErrStateLocked", err)
	}
	for _, want := range []string{"wait for the other run", "stale row", "fabrica-state-lock"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// Compile-time guard: the bridge depends on ResolveIdentity, which needs a
// provider that reports identity — TestProvider satisfies it.
var _ = func() { _ = config.Defaults() }

// identityFailLockingProvider has lock capability but a failing Identity.
type identityFailLockingProvider struct {
	lockingProvider
	identityErr error
}

func (p *identityFailLockingProvider) Identity(context.Context) (string, string, string, error) {
	return "", "", "", p.identityErr
}

func TestAcquireStateLockIdentityError(t *testing.T) {
	rt := testRuntimeWithLocker(&fakeLockManager{})
	rt.Provider = &identityFailLockingProvider{
		lockingProvider: lockingProvider{TestProvider: &testutil.TestProvider{}, fakeLockManager: &fakeLockManager{}},
		identityErr:     errors.New("creds gone"),
	}
	_, _, err := AcquireStateLock(context.Background(), rt, "op")
	if err == nil || !strings.Contains(err.Error(), "resolving account for state lock") {
		t.Fatalf("err = %v, want identity-failure context", err)
	}
}

func TestAcquireStateLockNilConfigIsNoop(t *testing.T) {
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)
	rt.Config = nil
	_, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("nil config must no-op: %v", err)
	}
	release()
	if locker.acquires.Load() != 0 {
		t.Errorf("acquires = %d, want 0", locker.acquires.Load())
	}
}

func TestAcquireStateLockReleaseWarnPath(t *testing.T) {
	locker := &fakeLockManager{releaseErr: errors.New("dynamo down")} // non-held: real release failure
	rt := testRuntimeWithLocker(locker)
	_, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	release() // must log via oplog and not panic or propagate
}

func TestAcquireStateLockGenericErrorWrapped(t *testing.T) {
	locker := &fakeLockManager{acquireErr: errors.New("dynamo down")}
	rt := testRuntimeWithLocker(locker)
	_, _, err := AcquireStateLock(context.Background(), rt, "op")
	if err == nil || !strings.Contains(err.Error(), "acquiring state lock (op)") {
		t.Fatalf("err = %v, want generic acquire wrap", err)
	}
}

func TestShortHostnameTrimsDomain(t *testing.T) {
	if got := shortHostname("runner-01.github.actions"); got != "runner-01" {
		t.Errorf("shortHostname(fqdn) = %q, want runner-01", got)
	}
	if got := shortHostname("bare-host"); got != "bare-host" {
		t.Errorf("shortHostname(bare) = %q, want bare-host", got)
	}
}

func TestAcquireStateLockUnlessDryRunSkipsAcquire(t *testing.T) {
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)
	ctx, release, err := AcquireStateLockUnlessDryRun(context.Background(), rt, "setup", true)
	if err != nil {
		t.Fatalf("dry-run lock: %v", err)
	}
	release()
	if locker.acquires.Load() != 0 {
		t.Errorf("dry-run acquires = %d, want 0", locker.acquires.Load())
	}
	if ctx.Value(lockHeldKey) != nil {
		t.Error("dry-run must not set the lock sentinel")
	}
}

func TestAcquireStateLockUnlessDryRunAcquiresWhenLive(t *testing.T) {
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)
	_, release, err := AcquireStateLockUnlessDryRun(context.Background(), rt, "setup", false)
	if err != nil {
		t.Fatalf("live lock: %v", err)
	}
	release()
	if locker.acquires.Load() != 1 {
		t.Errorf("live acquires = %d, want 1", locker.acquires.Load())
	}
}

func TestAcquireStateLockMissingTableProceedsUnlocked(t *testing.T) {
	locker := &fakeLockManager{acquireErr: cloud.ErrLockTableMissing}
	rt := testRuntimeWithLocker(locker)
	ctx, release, err := AcquireStateLock(context.Background(), rt, "setup")
	if err != nil {
		t.Fatalf("missing lock table must proceed unlocked, got: %v", err)
	}
	release()
	if ctx.Value(lockHeldKey) != nil {
		t.Error("sentinel must not be set when running unlocked")
	}
}

// withShortStateLockTTL shortens the lease so the heartbeat lapses inside
// the test window. It restores the default on return.
func withShortStateLockTTL(t *testing.T, ttl time.Duration) {
	t.Helper()
	old := stateLockTTL
	stateLockTTL = ttl
	t.Cleanup(func() { stateLockTTL = old })
}

// TestAcquireStateLockTakeoverCancelsCommandContext verifies that a
// confirmed takeover (renew -> ErrLockHeld) cancels the context the command
// runs its mutations on, aborting in-flight work.
func TestAcquireStateLockTakeoverCancelsCommandContext(t *testing.T) {
	withShortStateLockTTL(t, 30*time.Millisecond)
	locker := &fakeLockManager{renewErr: cloud.ErrLockHeld}
	rt := testRuntimeWithLocker(locker)

	ctx, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("command context was not cancelled after confirmed takeover")
	}
}

// TestAcquireStateLockTransientRenewLossCancelsAtLeaseLapse verifies that a
// run of transient renew failures cancels the command context once the lease
// margin lapses — the loss is bounded by the lease, not by the failure count.
func TestAcquireStateLockTransientRenewLossCancelsAtLeaseLapse(t *testing.T) {
	withShortStateLockTTL(t, 90*time.Millisecond)
	locker := &fakeLockManager{renewErr: errors.New("dynamo throttled")}
	rt := testRuntimeWithLocker(locker)

	ctx, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	start := time.Now()
	select {
	case <-ctx.Done():
		// Lease margin is ttl - ttl/10 = 81ms; allow generous slack but
		// still bound the wait so a missing abort fails the test.
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("abort took %v, want well under a second", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("command context was not cancelled after the lease lapsed")
	}
}

// TestAcquireStateLockHealthyHeartbeatKeepsContext verifies that a healthy
// heartbeat (succeeding renewals) keeps the command context alive and the
// renew calls flow through the lock manager.
func TestAcquireStateLockHealthyHeartbeatKeepsContext(t *testing.T) {
	withShortStateLockTTL(t, 200*time.Millisecond)
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)

	ctx, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	// Renewal evidence on a bounded deadline: a healthy heartbeat renews
	// every ~67 ms, so CI jitter cannot look like a dead heartbeat, and
	// each successful renewal re-extends the 180 ms lease margin.
	deadline := time.Now().Add(3 * time.Second)
	for locker.renews.Load() < 2 {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Settle inside the lease the last observed renewal just extended.
	time.Sleep(50 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatalf("command context cancelled by healthy heartbeat: %v", ctx.Err())
	}
	if locker.renews.Load() < 2 {
		t.Errorf("renews = %d, want at least 2", locker.renews.Load())
	}
}

// TestAcquireStateLockReleaseJoinsHeartbeat verifies that release waits for
// the heartbeat goroutine to exit, so no renewal (or lock logging) outlives
// the release — the leftover goroutine that raced a later test's oplog
// reset under -race.
func TestAcquireStateLockReleaseJoinsHeartbeat(t *testing.T) {
	withShortStateLockTTL(t, 200*time.Millisecond)
	locker := &fakeLockManager{}
	rt := testRuntimeWithLocker(locker)

	_, release, err := AcquireStateLock(context.Background(), rt, "op")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Heartbeat interval is ttl/3 (~67 ms); wait for at least one renewal,
	// then release and pin the renewal count.
	deadline := time.Now().Add(2 * time.Second)
	for locker.renews.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	release()
	settled := locker.renews.Load()

	// Well past two heartbeat intervals: a heartbeat that outlived the
	// release would have renewed again by now.
	time.Sleep(150 * time.Millisecond)
	if got := locker.renews.Load(); got != settled {
		t.Errorf("renews after release = %d, want %d (heartbeat must not outlive release)", got, settled)
	}
	if locker.releases.Load() != 1 {
		t.Errorf("releases = %d, want 1", locker.releases.Load())
	}
}
