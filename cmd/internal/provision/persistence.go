package provision

import (
	"context"
	"errors"
	"fmt"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// StateWriteRequirement selects whether canonical remote persistence is required.
type StateWriteRequirement uint8

const (
	StateWriteAllowLocal StateWriteRequirement = iota
	StateWriteRequireRemote
)

// StateWriteOutcome reports storage success for this invocation, not a prior
// write. RemotePersisted is true only after the canonical remote Put succeeds.
type StateWriteOutcome struct {
	RemotePersisted bool
}

// StatePersistence binds the canonical writer to a context and resolved target.
// Callers needing an acquired mutation context must supply it at construction.
type StatePersistence struct {
	ctx         context.Context
	path        string
	store       cloud.StateStore
	bucket      string
	account     string
	region      string
	table       string
	identity    tags.Identity
	identityErr error
}

// NewStatePersistence captures the same target and ownership as StateWriter.
// It performs no storage writes or state binding.
func NewStatePersistence(ctx context.Context, rt globals.Runtime) *StatePersistence {
	store, bucket := stateStore(rt)
	identity, identityErr := rt.Config.ResolveIdentity()
	account, region := identitySides(rt)
	return &StatePersistence{
		ctx: ctx, path: StatePath(rt), store: store, bucket: bucket,
		account: account, region: region, table: state.ResolveBackendNames(rt.Config, account).Table,
		identity: identity, identityErr: identityErr,
	}
}

// Persist validates and binds compatible state, writes remote state first,
// then refreshes the local cache. AllowLocal preserves bootstrap fallback;
// RequireRemote refuses unavailable storage rather than degrading to local-only.
// Custom ownership always requires remote storage and a live acquired lease.
// A cache failure after remote success remains a warning and successful outcome.
func (p *StatePersistence) Persist(st *state.State, requirement StateWriteRequirement) (StateWriteOutcome, error) {
	if err := p.ctx.Err(); err != nil {
		return StateWriteOutcome{}, err
	}
	if p.identityErr != nil {
		return StateWriteOutcome{}, p.identityErr
	}
	if requirement != StateWriteAllowLocal && requirement != StateWriteRequireRemote {
		return StateWriteOutcome{}, fmt.Errorf("unknown state persistence requirement %d; choose local fallback or required remote persistence", requirement)
	}
	ctx := p.ctx
	if p.identity.ManagedBy() != tags.ManagedByValue {
		lease, live := liveStateLock(ctx)
		if !live {
			return StateWriteOutcome{}, fmt.Errorf("custom ownership requires a live acquired state lock; run setup and acquire the account lock before persisting")
		}
		if !lease.matchesTarget(p.account, p.region, p.table) {
			return StateWriteOutcome{}, fmt.Errorf("custom state persistence requires the configured account, home region and table's acquired lock; acquire the target lock before persisting")
		}
		if err := state.ValidateIdentity(st, lease.account, lease.region); err != nil {
			return StateWriteOutcome{}, fmt.Errorf("validating custom state target: %w", err)
		}
		requirement = StateWriteRequireRemote
		// Preserve the caller's deadlines/values while linking even WithoutCancel
		// derivatives to cancellation of the actual acquired lease.
		writeCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lease.ctx, cancel)
		defer stop()
		defer cancel()
		ctx = writeCtx
	}
	if requirement == StateWriteRequireRemote && p.store == nil {
		return StateWriteOutcome{}, fmt.Errorf("canonical remote state storage unavailable; resolve the state bucket and use a provider with state storage before persisting")
	}
	if err := ctx.Err(); err != nil {
		return StateWriteOutcome{}, err
	}
	if err := st.BindOwnership(p.identity); err != nil {
		return StateWriteOutcome{}, err
	}
	outcome := StateWriteOutcome{}
	if p.store != nil {
		err := p.store.PutStateDocument(ctx, p.bucket, state.StateDocumentKey, state.EncodeState(st))
		switch {
		case err == nil:
			outcome.RemotePersisted = true
		case errors.Is(err, cloud.ErrStateBackendAbsent) && requirement == StateWriteAllowLocal:
			oplog.Logger().Warn("remote state bucket not found; keeping state in the local cache only", "bucket", p.bucket)
		default:
			return outcome, fmt.Errorf("persisting remote state: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	if err := state.WriteStateTo(st, p.path); err != nil {
		if outcome.RemotePersisted {
			oplog.Logger().Warn("remote state persisted; local cache write failed", "file", p.path, "error", err)
			return outcome, nil
		}
		return outcome, err
	}
	return outcome, nil
}
