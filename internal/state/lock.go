package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

// DefaultLockTTL is how long a state lock remains valid without renewal.
// A crashed holder must not deadlock everyone forever: once the TTL lapses,
// another run takes the lock over automatically (with an oplog warning).
// Live holders must call Renew before this window elapses (see
// provision.AcquireStateLock heartbeat) so a long promote cannot be taken over.
const DefaultLockTTL = 15 * time.Minute

// RenewCondition is the DynamoDB expression LockStore.Renew passes to put.
// "#tok" maps to the Token attribute (reserved word) in the AWS adapter.
const RenewCondition = "#tok = :token"

// PutItemFunc performs the conditional put that backs Acquire. condition uses
// DynamoDB expression syntax; condValues carries its ":name" values.
type PutItemFunc func(ctx context.Context, item map[string]string, condition string, condValues map[string]string) error

// DeleteItemFunc performs the conditional delete that backs Release.
type DeleteItemFunc func(ctx context.Context, key map[string]string, condition string, condValues map[string]string) error

// LockStore is the DynamoDB-backed distributed lock guarding Fabrica's remote
// and local state against concurrent runs. Storage mechanics are injected as
// functions so the SDK stays out of this package; internal/cloud/aws provides
// the DynamoDB implementation via cloud.StateLockManager.
type LockStore struct {
	table     string
	ttl       time.Duration
	now       func() time.Time
	newTicker TickerFunc
	newTimer  TimerFunc
	put       PutItemFunc
	del       DeleteItemFunc
}

// Timer is the lease-timer surface Heartbeat re-arms; *time.Timer satisfies it.
type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// TickerFunc starts a periodic tick source of period d and returns its channel
// and a stop function. TimerFunc starts a one-shot timer of duration d and
// returns its channel and handle. Both are the seams that let tests drive
// heartbeat renewals and lease expiry exactly, instead of racing the scheduler.
type (
	TickerFunc func(d time.Duration) (ticks <-chan time.Time, stop func())
	TimerFunc  func(d time.Duration) (fire <-chan time.Time, t Timer)
)

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func realTimer(d time.Duration) (<-chan time.Time, Timer) {
	t := time.NewTimer(d)
	return t.C, t
}

// NewFuncLockStore builds a LockStore over injected storage functions.
// ttl <= 0 selects DefaultLockTTL.
func NewFuncLockStore(table string, ttl time.Duration, put PutItemFunc, del DeleteItemFunc) *LockStore {
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &LockStore{
		table:     table,
		ttl:       ttl,
		now:       time.Now,
		newTicker: realTicker,
		newTimer:  realTimer,
		put:       put,
		del:       del,
	}
}

// SetClock overrides the time source (tests).
func (s *LockStore) SetClock(now func() time.Time) { s.now = now }

// SetTicker overrides the heartbeat renewal tick source (tests).
func (s *LockStore) SetTicker(newTicker TickerFunc) { s.newTicker = newTicker }

// SetTimer overrides the heartbeat lease timer (tests).
func (s *LockStore) SetTimer(newTimer TimerFunc) { s.newTimer = newTimer }

// Acquire attempts to acquire a lock for resourceID held by holder.
// Succeeds when the lock row is absent OR older than the TTL (stale takeover).
// Returns the release token on success.
func (s *LockStore) Acquire(ctx context.Context, resourceID, holder string) (string, error) {
	token, err := genToken()
	if err != nil {
		return "", fmt.Errorf("generating lock token: %w", err)
	}

	now := s.now().UTC()
	item := map[string]string{
		"LockID":     resourceID,
		"Holder":     holder,
		"Token":      token,
		"AcquiredAt": strconv.FormatInt(now.Unix(), 10),
	}
	staleBefore := strconv.FormatInt(now.Add(-s.ttl).Unix(), 10)

	err = s.put(ctx, item,
		"attribute_not_exists(LockID) OR AcquiredAt <= :stale",
		map[string]string{":stale": staleBefore},
	)
	if err != nil {
		oplog.WithResource("DynamoDB:Lock", resourceID).Warn("lock not acquired", "holder", holder, "error", err)
		return "", fmt.Errorf("acquiring lock %s: %w", resourceID, err)
	}

	oplog.WithResource("DynamoDB:Lock", resourceID).Debug("lock acquired", "holder", holder)
	return token, nil
}

// Renew bumps AcquiredAt so a live holder is not treated as stale. Only the
// current token can renew. The error is wrapped with %w, so callers can
// detect a confirmed takeover via errors.Is(err, fabricac.ErrLockHeld) and
// distinguish it from transient storage failures.
func (s *LockStore) Renew(ctx context.Context, resourceID, token, holder string) error {
	now := s.now().UTC()
	item := map[string]string{
		"LockID":     resourceID,
		"Holder":     holder,
		"Token":      token,
		"AcquiredAt": strconv.FormatInt(now.Unix(), 10),
	}
	err := s.put(ctx, item, RenewCondition, map[string]string{":token": token})
	if err != nil {
		oplog.WithResource("DynamoDB:Lock", resourceID).Warn("lock not renewed", "holder", holder, "error", err)
		return fmt.Errorf("renewing lock %s: %w", resourceID, err)
	}
	oplog.WithResource("DynamoDB:Lock", resourceID).Debug("lock renewed", "holder", holder)
	return nil
}

// Heartbeat renews the lock every ttl/3 until ctx is cancelled, propagating
// lock loss to onLost so the caller can abort in-flight work. A confirmed
// takeover (fabricac.ErrLockHeld) fires onLost immediately — the row belongs
// to someone else and further renewals will never work. Transient storage
// errors are retried on the next tick, but the lease is only extended by
// successful renewals: if the lease (last successful renewal + ttl, minus a
// ttl/10 margin) lapses without one, onLost fires with the last error.
// onLost fires at most once and may be nil.
func (s *LockStore) Heartbeat(ctx context.Context, resourceID, token, holder string, onLost func(error)) {
	lost := &sync.Once{}
	fireLost := func(err error) {
		if onLost == nil {
			return
		}
		lost.Do(func() { onLost(err) })
	}

	interval := s.ttl / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticks, stop := s.newTicker(interval)
	defer stop()

	// margin keeps the abort ahead of the real expiry so the row is still
	// ours (or at least ambiguous) when the caller stops mutating.
	margin := s.ttl / 10
	if margin <= 0 {
		margin = time.Millisecond
	}
	deadline := s.now().Add(s.ttl)
	leaseFired, leaseTimer := s.newTimer(leaseDelay(deadline, margin))
	defer leaseTimer.Stop()
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			return
		case <-leaseFired:
			if lastErr == nil {
				lastErr = fmt.Errorf("lock lease lapsed without a successful renewal")
			}
			fireLost(lastErr)
			return
		case <-ticks:
			err := s.Renew(ctx, resourceID, token, holder)
			switch {
			case err == nil:
				lastErr = nil
				deadline = s.now().Add(s.ttl)
				if !rescheduleLease(leaseTimer, deadline, margin) {
					fireLost(fmt.Errorf("lock lease margin lapsed immediately after renewal"))
					return
				}
			case errors.Is(err, fabricac.ErrLockHeld):
				fireLost(err)
				return
			default:
				lastErr = err
			}
		}
	}
}

// leaseDelay is the wait until the lease margin (deadline minus margin)
// lapses, clamped to zero.
func leaseDelay(deadline time.Time, margin time.Duration) time.Duration {
	d := time.Until(deadline) - margin
	if d < 0 {
		return 0
	}
	return d
}

// rescheduleLease moves the lease timer to margin before the new deadline,
// reporting false when the margin has already lapsed (backward clock jump or
// already-expired timer), in which case the caller must abort. Since Go 1.23
// timer channels are unbuffered and the value reaches only the heartbeat's
// own lease case — which aborts first — the expired branch is defensive.
func rescheduleLease(leaseTimer Timer, deadline time.Time, margin time.Duration) bool {
	d := leaseDelay(deadline, margin)
	if d == 0 {
		return false
	}
	if !leaseTimer.Stop() {
		// Expired or stopped: re-arming would mutate past a lapsed lease.
		return false
	}
	leaseTimer.Reset(d)
	return true
}

// Release releases a previously acquired lock. Only the holder with the
// matching token can release; a failed conditional means someone else took
// the lock over after the TTL lapsed, which is not an error for the caller.
func (s *LockStore) Release(ctx context.Context, resourceID, token string) error {
	key := map[string]string{"LockID": resourceID}

	err := s.del(ctx, key, "Token = :token", map[string]string{":token": token})
	if err != nil {
		oplog.WithResource("DynamoDB:Lock", resourceID).Warn("release did not delete row (takeover likely)", "error", err)
		return nil
	}
	oplog.WithResource("DynamoDB:Lock", resourceID).Debug("lock released")
	return nil
}

func genToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	return hex.EncodeToString(b), nil
}
