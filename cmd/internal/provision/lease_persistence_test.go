package provision

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// Only renewal failure injection is local; conditional acquire/release rows are
// the canonical fake's real row mechanics, used by actual AcquireStateLock.
type leasePersistenceProvider struct {
	*testutil.BackendLockingProvider
	renewErr   error
	putStarted chan context.Context
	putUnblock chan struct{}
}

func (p *leasePersistenceProvider) AcquireStateLockRow(ctx context.Context, table string, item map[string]string, condition string, values map[string]string) error {
	if condition == state.RenewCondition && p.renewErr != nil {
		return p.renewErr
	}
	return p.BackendLockingProvider.AcquireStateLockRow(ctx, table, item, condition, values)
}

func (p *leasePersistenceProvider) PutStateDocument(ctx context.Context, bucket, key string, data []byte) error {
	if p.putStarted != nil {
		p.putStarted <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.putUnblock:
			return context.Canceled
		}
	}
	return p.BackendLockingProvider.PutStateDocument(ctx, bucket, key, data)
}

func leasePersistenceRuntime(t *testing.T) (globals.Runtime, *leasePersistenceProvider) {
	t.Helper()
	p := &leasePersistenceProvider{BackendLockingProvider: &testutil.BackendLockingProvider{BackendProvider: &testutil.BackendProvider{
		TestProvider: &testutil.TestProvider{}, Bucket: cloud.StateBackendMetadata{Exists: true}, Table: cloud.StateBackendMetadata{Exists: true},
	}}}
	rt := stateTestRuntime(t, p.TestProvider)
	rt.Provider = p
	return rt, p
}

func waitLeaseLoss(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("real heartbeat did not cancel acquired context")
	}
}

func TestStateLockHeldTracksActualLeaseThroughWithoutCancel(t *testing.T) {
	for _, loss := range []string{"release", "parent cancellation", "takeover", "lapse"} {
		t.Run(loss, func(t *testing.T) {
			withShortStateLockTTL(t, 90*time.Millisecond)
			rt, p := leasePersistenceRuntime(t)
			if loss == "lapse" {
				p.renewErr = errors.New("renew throttled")
			}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			acquired, release, err := AcquireStateLock(parent, rt, "lease contract")
			if err != nil {
				t.Fatal(err)
			}
			released := false
			defer func() {
				if !released {
					release()
				}
			}()
			detached := context.WithoutCancel(acquired)
			timeout, stop := context.WithTimeout(acquired, time.Second)
			defer stop()
			nested, nestedRelease, err := AcquireStateLock(timeout, rt, "nested")
			if err != nil || nested != timeout || !StateLockHeld(detached) || !StateLockHeld(nested) {
				t.Fatalf("live nested lease lost: %v", err)
			}
			nestedRelease()
			row := p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)
			if row["Token"] == "" {
				t.Fatal("no actual conditional row")
			}
			switch loss {
			case "release":
				release()
				released = true
			case "parent cancellation":
				cancel()
			case "takeover":
				takeover := map[string]string{"LockID": "fabrica-state/" + stateTestAccount, "Token": "other-token", "AcquiredAt": "0"}
				err = p.BackendLockingProvider.AcquireStateLockRow(context.Background(), rt.Config.State.Table, takeover, "attribute_not_exists(LockID) OR AcquiredAt <= :stale", map[string]string{":stale": "9999999999"})
				if err != nil {
					t.Fatal(err)
				}
				waitLeaseLoss(t, acquired)
			case "lapse":
				waitLeaseLoss(t, acquired)
			}
			if detached.Err() != nil {
				t.Fatal("WithoutCancel unexpectedly cancelled")
			}
			if StateLockHeld(acquired) || StateLockHeld(detached) || StateLockHeld(timeout) {
				t.Fatal("dead real lease was revived by derived context")
			}
			_, cleanup, err := AcquireStateLock(detached, rt, "stale nested")
			cleanup()
			if err == nil {
				t.Fatal("stale retained marker authorized nested acquisition")
			}
			if !released {
				release()
				released = true
			}
			if loss == "takeover" {
				if p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)["Token"] != "other-token" {
					t.Fatal("release removed takeover row")
				}
			} else if len(p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)) != 0 {
				t.Fatal("cleanup failed to release real row")
			}
		})
	}
}

// Config's public custom guard stays active. These contract tests select an
// immutable private identity in the actual storage owner, never a CLI factory
// bypass or fabricated lock sentinel; all acquired contexts come from real rows.
func TestCustomPersistenceRequiresLiveLeaseAndRemoteStorage(t *testing.T) {
	owner, err := tags.ResolveIdentity("Studio-Tools")
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range []StateWriteRequirement{StateWriteAllowLocal, StateWriteRequireRemote} {
		for _, scenario := range []string{"initial", "no lease", "no capability", "missing table", "cancelled", "released", "detached released", "takeover", "lapse", "no store", "absent bucket", "remote failure", "cache failure", "unknown requirement"} {
			t.Run(map[StateWriteRequirement]string{StateWriteAllowLocal: "allow local", StateWriteRequireRemote: "require remote"}[requirement]+"/"+scenario, func(t *testing.T) {
				t.Chdir(t.TempDir())
				logs := captureLogs(t)
				rt, p := leasePersistenceRuntime(t)
				if scenario == "lapse" {
					withShortStateLockTTL(t, 90*time.Millisecond)
					p.renewErr = errors.New("renew throttled")
				}
				if scenario == "takeover" {
					withShortStateLockTTL(t, 90*time.Millisecond)
				}
				if scenario == "no capability" {
					rt.Provider = struct{ cloud.Provider }{p}
				}
				if scenario == "missing table" {
					p.Table.Exists = false
				}
				ctx := context.Background()
				release := func() {}
				if scenario != "no lease" {
					var err error
					ctx, release, err = AcquireStateLock(ctx, rt, "private persistence contract")
					if err != nil {
						t.Fatal(err)
					}
				}
				released := false
				defer func() {
					if !released {
						release()
					}
				}()
				writer := NewStatePersistence(ctx, rt)
				writer.identity = owner
				if scenario == "no store" {
					writer.store = nil
				}
				if scenario == "absent bucket" {
					p.Bucket.Exists = false
				}
				if scenario == "remote failure" {
					p.StateStorePutErr = errors.New("storage unavailable")
				}
				if scenario == "cache failure" {
					blockLocalCache(t)
				}
				switch scenario {
				case "released", "detached released":
					release()
					released = true
					if scenario == "detached released" {
						writer.ctx = context.WithoutCancel(ctx)
					}
				case "cancelled":
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					writer.ctx = cancelled
				case "takeover":
					takeover := map[string]string{"LockID": "fabrica-state/" + stateTestAccount, "Token": "other-token", "AcquiredAt": "0"}
					if err := p.BackendLockingProvider.AcquireStateLockRow(context.Background(), rt.Config.State.Table, takeover, "attribute_not_exists(LockID) OR AcquiredAt <= :stale", map[string]string{":stale": "9999999999"}); err != nil {
						t.Fatal(err)
					}
					waitLeaseLoss(t, ctx)
					writer.ctx = context.WithoutCancel(ctx)
				case "lapse":
					waitLeaseLoss(t, ctx)
					writer.ctx = context.WithoutCancel(ctx)
				}
				requested := requirement
				if scenario == "unknown requirement" {
					requested = StateWriteRequirement(255)
				}
				st := state.NewState(stateTestAccount, "us-east-1")
				st.UpsertModule("lore", "test", "ready", nil)
				st.AddOp("lore", "create", 0)
				outcome, err := writer.Persist(st, requested)
				success := scenario == "initial" || scenario == "cache failure"
				if (err == nil) != success || outcome.RemotePersisted != success {
					t.Fatalf("outcome=%+v error=%v", outcome, err)
				}
				validationFailure := scenario != "initial" && scenario != "cache failure" && scenario != "absent bucket" && scenario != "remote failure"
				if validationFailure && st.ManagedBy != "" {
					t.Fatal("failed lease/destination validation bound state")
				}
				if success {
					remote, parseErr := state.ParseState([]byte(p.StateDocuments[stateTestKey()]), stateTestAccount, "us-east-1")
					if parseErr != nil || remote.ManagedBy != owner.ManagedBy() || remote.GetModule("lore") == nil || len(remote.History) != 1 {
						t.Fatalf("remote contract lost: %+v %v", remote, parseErr)
					}
					if scenario == "cache failure" && !strings.Contains(logs.String(), "local cache write failed") {
						t.Fatal("remote success hid cache failure")
					}
					if scenario == "initial" {
						before, readErr := os.ReadFile(StatePath(rt))
						if readErr != nil {
							t.Fatal(readErr)
						}
						remoteBefore := p.StateDocuments[stateTestKey()]
						p.Bucket.Exists = false
						second, err := writer.Persist(st, StateWriteAllowLocal)
						after, readErr := os.ReadFile(StatePath(rt))
						if !errors.Is(err, cloud.ErrStateBackendAbsent) || second.RemotePersisted || readErr != nil || string(after) != string(before) || p.StateDocuments[stateTestKey()] != remoteBefore {
							t.Fatalf("failed second write degraded/reused outcome: %+v %v", second, err)
						}
					}
				} else {
					if len(p.StateDocuments) != 0 {
						t.Fatal("failed custom persistence wrote canonical document")
					}
					if _, err := os.Stat(StatePath(rt)); !os.IsNotExist(err) {
						t.Fatalf("failed custom write refreshed cache: %v", err)
					}
				}
				if !released {
					release()
					released = true
				}
				puts := 0
				for _, call := range p.BackendCalls {
					if call.Operation == "put document" {
						puts++
					}
				}
				if validationFailure && puts != 0 {
					t.Fatal("lease/storage validation reached remote write")
				}
			})
		}
	}
}

func TestCancelledDefaultLocalPersistenceLeavesStateAndCacheUntouched(t *testing.T) {
	for _, adapter := range []bool{false, true} {
		t.Run(map[bool]string{false: "engine", true: "adapter"}[adapter], func(t *testing.T) {
			t.Chdir(t.TempDir())
			rt, _ := leasePersistenceRuntime(t)
			rt.Provider = nil
			before := state.NewState(stateTestAccount, "us-east-1")
			if err := state.WriteStateTo(before, StatePath(rt)); err != nil {
				t.Fatal(err)
			}
			cache, err := os.ReadFile(StatePath(rt))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			st := state.NewState(stateTestAccount, "us-east-1")
			if adapter {
				err = StateWriter(ctx, rt)(st)
			} else {
				_, err = NewStatePersistence(ctx, rt).Persist(st, StateWriteAllowLocal)
			}
			after, readErr := os.ReadFile(StatePath(rt))
			if !errors.Is(err, context.Canceled) || st.ManagedBy != "" || readErr != nil || string(after) != string(cache) {
				t.Fatalf("cancelled local write changed state/cache: %v %v", err, readErr)
			}
		})
	}
}

func TestCustomPersistenceDetachedCallerCancelsDuringRemoteWrite(t *testing.T) {
	for _, loss := range []string{"release", "heartbeat", "caller deadline"} {
		t.Run(loss, func(t *testing.T) {
			t.Chdir(t.TempDir())
			rt, p := leasePersistenceRuntime(t)
			if loss == "heartbeat" {
				withShortStateLockTTL(t, 90*time.Millisecond)
				p.renewErr = errors.New("renew throttled")
			}
			ctx, release, err := AcquireStateLock(context.Background(), rt, "in-flight storage")
			if err != nil {
				t.Fatal(err)
			}
			released := false
			defer func() {
				if !released {
					release()
				}
			}()
			p.putStarted = make(chan context.Context, 1)
			p.putUnblock = make(chan struct{})
			writerCtx := context.WithoutCancel(ctx)
			wantErr := context.Canceled
			if loss == "caller deadline" {
				var cancel context.CancelFunc
				writerCtx, cancel = context.WithTimeout(writerCtx, 100*time.Millisecond)
				defer cancel()
				wantErr = context.DeadlineExceeded
			}
			writer := NewStatePersistence(writerCtx, rt)
			writer.identity, err = tags.ResolveIdentity("Studio-Tools")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := writer.Persist(state.NewState(stateTestAccount, "us-east-1"), StateWriteAllowLocal)
				done <- err
			}()
			var seen context.Context
			select {
			case seen = <-p.putStarted:
			case <-time.After(time.Second):
				close(p.putUnblock)
				t.Fatal("actual Put did not start")
			}
			switch loss {
			case "release":
				release()
				released = true
			case "heartbeat":
				waitLeaseLoss(t, ctx)
			}
			select {
			case err = <-done:
				if !errors.Is(err, wantErr) || seen.Err() == nil {
					t.Fatalf("remote write ignored actual lease cancellation: %v", err)
				}
			case <-time.After(time.Second):
				close(p.putUnblock)
				err = <-done
				t.Fatalf("in-flight remote write survived actual lease loss via WithoutCancel: %v", err)
			}
			if len(p.StateDocuments) != 0 {
				t.Fatal("lost lease wrote remote state")
			}
			if _, err := os.Stat(StatePath(rt)); !os.IsNotExist(err) {
				t.Fatalf("lost lease refreshed cache: %v", err)
			}
		})
	}
}

func TestAcquiredLeaseRejectsOtherPersistenceTarget(t *testing.T) {
	for _, target := range []string{"account", "table", "region"} {
		t.Run(target, func(t *testing.T) {
			t.Chdir(t.TempDir())
			rt, p := leasePersistenceRuntime(t)
			ctx, release, err := AcquireStateLock(context.Background(), rt, "original target")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			other := rt
			cfg := *rt.Config
			other.Config = &cfg
			switch target {
			case "account":
				cfg.Cloud.AWS.AccountID = "999999999999"
			case "table":
				cfg.State.Table = "other-state-lock"
			case "region":
				cfg.Cloud.AWS.Region = "us-west-2"
			}
			_, cleanup, err := AcquireStateLock(ctx, other, "wrong nested target")
			cleanup()
			if err == nil {
				t.Error("nested acquisition accepted another account/table/region")
			}
			writer := NewStatePersistence(ctx, other)
			writer.identity, err = tags.ResolveIdentity("Studio-Tools")
			if err != nil {
				t.Fatal(err)
			}
			st := state.NewState(stateTestAccount, "us-east-1")
			outcome, err := writer.Persist(st, StateWriteAllowLocal)
			if err == nil || outcome.RemotePersisted || st.ManagedBy != "" || len(p.StateDocuments) != 0 {
				t.Fatalf("wrong target bound or persisted state: %+v %v", outcome, err)
			}
			if _, err := os.Stat(StatePath(other)); !os.IsNotExist(err) {
				t.Fatalf("wrong target refreshed cache: %v", err)
			}
			if !StateLockHeld(ctx) || p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)["Token"] == "" {
				t.Fatal("target refusal changed the original live lease")
			}
		})
	}
}

func TestCustomPersistenceCapturesLeaseTarget(t *testing.T) {
	for _, unknown := range []string{"neither", "account", "region", "both"} {
		t.Run("unknown "+unknown, func(t *testing.T) {
			t.Chdir(t.TempDir())
			rt, p := leasePersistenceRuntime(t)
			ctx, release, err := AcquireStateLock(context.Background(), rt, "captured target")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			// An omitted table resolves through the same canonical backend names.
			rt.Config.State.Table = ""
			nested, cleanup, err := AcquireStateLock(ctx, rt, "default table")
			cleanup()
			if err != nil || nested != ctx {
				t.Fatalf("canonical default table changed the lease target: %v", err)
			}
			if unknown == "account" || unknown == "both" {
				rt.Config.Cloud.AWS.AccountID = ""
				rt.Config.State.Bucket = stateTestBucket()
			}
			if unknown == "region" || unknown == "both" {
				rt.Config.Cloud.AWS.Region = ""
			}
			writer := NewStatePersistence(ctx, rt)
			writer.identity, err = tags.ResolveIdentity("Studio-Tools")
			if err != nil {
				t.Fatal(err)
			}
			// A writer retains its original target despite later config mutation.
			rt.Config.Cloud.AWS.AccountID = "999999999999"
			rt.Config.State.Table = "other-state-lock"
			rt.Config.Cloud.AWS.Region = "us-west-2"
			outcome, err := writer.Persist(state.NewState(stateTestAccount, "us-east-1"), StateWriteAllowLocal)
			if err != nil || !outcome.RemotePersisted || p.StateDocuments[stateTestKey()] == "" {
				t.Fatalf("captured original target changed: %+v %v", outcome, err)
			}
		})
	}
}

func TestAcquireStateLockCancelledCallerDoesNotCreateRow(t *testing.T) {
	rt, p := leasePersistenceRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	acquired, release, err := AcquireStateLock(ctx, rt, "cancelled acquisition")
	release()
	if !errors.Is(err, context.Canceled) || acquired != ctx || StateLockHeld(acquired) {
		t.Fatalf("cancelled caller acquired a lease: %v", err)
	}
	if len(p.BackendCalls) != 0 || len(p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)) != 0 {
		t.Fatal("cancelled caller reached conditional lock storage")
	}
}

func TestCustomPersistenceRejectsForeignStateTarget(t *testing.T) {
	for _, requirement := range []StateWriteRequirement{StateWriteAllowLocal, StateWriteRequireRemote} {
		for _, unknown := range []bool{false, true} {
			for _, side := range []string{"account", "region"} {
				t.Run(map[StateWriteRequirement]string{StateWriteAllowLocal: "allow local", StateWriteRequireRemote: "require remote"}[requirement]+"/"+map[bool]string{false: "known config", true: "omitted config"}[unknown]+"/"+side, func(t *testing.T) {
					t.Chdir(t.TempDir())
					rt, p := leasePersistenceRuntime(t)
					ctx, release, err := AcquireStateLock(context.Background(), rt, "payload target")
					if err != nil {
						t.Fatal(err)
					}
					defer release()
					if unknown {
						rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region = "", ""
						rt.Config.State.Bucket = stateTestBucket()
					}
					writer := NewStatePersistence(ctx, rt)
					writer.identity, err = tags.ResolveIdentity("Studio-Tools")
					if err != nil {
						t.Fatal(err)
					}
					st := state.NewState(stateTestAccount, "us-east-1")
					if side == "account" {
						st.Account = "999999999999"
					} else {
						st.Region = "us-west-2"
					}
					outcome, err := writer.Persist(st, requirement)
					if err == nil || !strings.Contains(err.Error(), "state target mismatch") || outcome.RemotePersisted || st.ManagedBy != "" || len(p.StateDocuments) != 0 {
						t.Fatalf("foreign payload bound/persisted under the correct lease: %+v %v", outcome, err)
					}
					if err := st.ValidateOwnership(tags.Identity{}); err != nil {
						t.Fatalf("refusal changed private loaded binding: %v", err)
					}
					if _, err := os.Stat(StatePath(rt)); !os.IsNotExist(err) {
						t.Fatalf("foreign payload refreshed cache: %v", err)
					}
					if !StateLockHeld(ctx) || p.LockRow(rt.Config.State.Table, "fabrica-state/"+stateTestAccount)["Token"] == "" {
						t.Fatal("payload refusal changed the correct live lease")
					}
				})
			}
		}
	}
}

func TestCustomPersistenceAcceptsUnknownRecordedTargetSides(t *testing.T) {
	for _, side := range []string{"account", "region", "both"} {
		t.Run(side, func(t *testing.T) {
			t.Chdir(t.TempDir())
			rt, p := leasePersistenceRuntime(t)
			ctx, release, err := AcquireStateLock(context.Background(), rt, "unknown recorded side")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			writer := NewStatePersistence(ctx, rt)
			writer.identity, err = tags.ResolveIdentity("Studio-Tools")
			if err != nil {
				t.Fatal(err)
			}
			st := state.NewState(stateTestAccount, "us-east-1")
			if side == "account" || side == "both" {
				st.Account = ""
			}
			if side == "region" || side == "both" {
				st.Region = ""
			}
			outcome, err := writer.Persist(st, StateWriteAllowLocal)
			if err != nil || !outcome.RemotePersisted || st.ManagedBy != "Studio-Tools" {
				t.Fatalf("unknown recorded side became a mismatch: %+v %v", outcome, err)
			}
			remote, err := state.ParseState([]byte(p.StateDocuments[stateTestKey()]), stateTestAccount, "us-east-1")
			if err != nil || remote.Account != st.Account || remote.Region != st.Region {
				t.Fatalf("compatible unknown side was overwritten: %v", err)
			}
		})
	}
}
