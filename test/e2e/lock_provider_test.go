package e2e

import (
	"context"
	"maps"
	"strconv"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

func (p *fakeProvider) AcquireStateLockRow(ctx context.Context, table string, item map[string]string, condition string, values map[string]string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.store.tables[table] {
		return cloud.ErrLockTableMissing
	}
	key := table + "/" + item["LockID"]
	actual := p.store.lockRows[key]
	if condition == state.RenewCondition {
		if actual == nil || actual["Token"] != values[":token"] {
			return cloud.ErrLockHeld
		}
	} else if actual != nil {
		acquired, err := strconv.ParseInt(actual["AcquiredAt"], 10, 64)
		if err != nil {
			return err
		}
		stale, err := strconv.ParseInt(values[":stale"], 10, 64)
		if err != nil {
			return err
		}
		if acquired > stale {
			return cloud.ErrLockHeld
		}
	}
	p.store.lockRows[key] = maps.Clone(item)
	return nil
}

func (p *fakeProvider) ReleaseStateLockRow(ctx context.Context, table, id, token string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key := table + "/" + id
	if actual := p.store.lockRows[key]; actual == nil || actual["Token"] != token {
		return cloud.ErrLockHeld
	}
	delete(p.store.lockRows, key)
	return nil
}

var _ cloud.StateLockManager = (*fakeProvider)(nil)
