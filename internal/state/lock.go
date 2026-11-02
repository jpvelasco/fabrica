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
	table string
	ttl   time.Duration
	now   func() time.Time
	put   PutItemFunc
	del   DeleteItemFunc
}

// NewFuncLockStore builds a LockStore over injected storage functions.
// ttl <= 0 selects DefaultLockTTL.
func NewFuncLockStore(table string, ttl time.Duration, put PutItemFunc, del DeleteItemFunc) *LockStore {
	if ttl <= 0 {
		ttl = DefaultLockTTL
	}
	return &LockStore{
		table: table,
		ttl:   ttl,
		now:   time.Now,
		put:   put,
		del:   del,
	}
}

// SetClock overrides the time source (tests).
func (s *LockStore) SetClock(now func() time.Time) { s.now = now }

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
// lock loss to onLost so the caller can abort in-flight work:
//
//   - a confirmed takeover (fabricac.ErrLockHeld) fires onLost immediately —
//     the row belongs to someone else and further renewals will never work;
//   - transient storage errors are retried on the next tick, but the lease is
//     only extended by successful renewals: if the lease (last successful
//     renewal + ttl, minus a ttl/10 margin) lapses without one, onLost fires
//     with the last error, aborting before the lock actually expires.
//
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
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// margin keeps the abort ahead of the real expiry so the row is still
	// ours (or at least ambiguous) when the caller stops mutating.
	margin := s.ttl / 10
	if margin <= 0 {
		margin = time.Millisecond
	}
	deadline := s.now().Add(s.ttl)
	leaseTimer := time.NewTimer(leaseDelay(deadline, margin))
	defer leaseTimer.Stop()
	var lastErr error

	for {
		select {
		case <-ctx.Done():
			return
		case <-leaseTimer.C:
			if lastErr == nil {
				lastErr = fmt.Errorf("lock lease lapsed without a successful renewal")
			}
			fireLost(lastErr)
			return
		case <-ticker.C:
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

// rescheduleLease moves the lease timer to margin before the new deadline.
// It reports false when the margin has already lapsed — a clock that jumped
// past the deadline, or an already-expired timer — in which case the caller
// must abort. Since Go 1.23 timer channels are unbuffered: an expired lease
// timer hands its value only to a receiver already parked on it, which for
// the heartbeat is its own leaseTimer.C case — and that case aborts before
// any reschedule, so the expired branch below is defensive.
func rescheduleLease(leaseTimer *time.Timer, deadline time.Time, margin time.Duration) bool {
	d := leaseDelay(deadline, margin)
	if d == 0 {
		return false
	}
	if !leaseTimer.Stop() {
		// The timer already expired or was stopped: re-arming would keep
		// the caller mutating past a lease that may be gone, so report
		// loss instead.
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
