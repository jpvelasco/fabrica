package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// fakeLockManager records lock calls for bridge tests.
type fakeLockManager struct {
	acquires   int
	releases   int
	held       bool
	releaseErr error
	acquireErr error
}

func (f *fakeLockManager) AcquireStateLockRow(_ context.Context, _ string, _ map[string]string, _ string, _ map[string]string) error {
	f.acquires++
	if f.acquireErr != nil {
		return f.acquireErr
	}
	if f.held {
		return cloud.ErrLockHeld
	}
	return nil
}

func (f *fakeLockManager) ReleaseStateLockRow(_ context.Context, _, _, _ string) error {
	f.releases++
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
	if locker.acquires != 1 {
		t.Errorf("acquires = %d, want 1", locker.acquires)
	}
	release()
	if locker.releases != 1 {
		t.Errorf("releases = %d, want 1", locker.releases)
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
	if locker.acquires != 1 {
		t.Errorf("acquires = %d, want 1 (nested must not re-acquire)", locker.acquires)
	}
	if locker.releases != 0 {
		t.Errorf("releases = %d before outer release; nested release must be a no-op", locker.releases)
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
	if locker.acquires != 0 {
		t.Errorf("acquires = %d, want 0", locker.acquires)
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
