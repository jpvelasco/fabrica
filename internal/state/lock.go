package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/jpvelasco/fabrica/internal/oplog"
)

// DefaultLockTTL is how long a state lock remains valid without renewal.
// A crashed holder must not deadlock everyone forever: once the TTL lapses,
// another run takes the lock over automatically (with an oplog warning).
const DefaultLockTTL = 15 * time.Minute

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
