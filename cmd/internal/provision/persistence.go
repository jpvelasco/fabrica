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
	identity    tags.Identity
	identityErr error
}

// NewStatePersistence captures the same target and ownership as StateWriter.
// It performs no storage writes or state binding.
func NewStatePersistence(ctx context.Context, rt globals.Runtime) *StatePersistence {
	store, bucket := stateStore(rt)
	identity, identityErr := rt.Config.ResolveIdentity()
	return &StatePersistence{
		ctx: ctx, path: StatePath(rt), store: store, bucket: bucket,
		identity: identity, identityErr: identityErr,
	}
}

// Persist validates and binds compatible state, writes remote state first,
// then refreshes the local cache. AllowLocal preserves bootstrap fallback;
// RequireRemote refuses unavailable storage rather than degrading to local-only.
// A cache failure after remote success remains a warning and successful outcome.
func (p *StatePersistence) Persist(st *state.State, requirement StateWriteRequirement) (StateWriteOutcome, error) {
	if p.identityErr != nil {
		return StateWriteOutcome{}, p.identityErr
	}
	if requirement != StateWriteAllowLocal && requirement != StateWriteRequireRemote {
		return StateWriteOutcome{}, fmt.Errorf("unknown state persistence requirement %d; choose local fallback or required remote persistence", requirement)
	}
	if requirement == StateWriteRequireRemote && p.store == nil {
		return StateWriteOutcome{}, fmt.Errorf("canonical remote state storage unavailable; resolve the state bucket and use a provider with state storage before persisting")
	}
	if err := st.BindOwnership(p.identity); err != nil {
		return StateWriteOutcome{}, err
	}
	outcome := StateWriteOutcome{}
	if p.store != nil {
		err := p.store.PutStateDocument(p.ctx, p.bucket, state.StateDocumentKey, state.EncodeState(st))
		switch {
		case err == nil:
			outcome.RemotePersisted = true
		case errors.Is(err, cloud.ErrStateBackendAbsent) && requirement == StateWriteAllowLocal:
			oplog.Logger().Warn("remote state bucket not found; keeping state in the local cache only", "bucket", p.bucket)
		default:
			return outcome, fmt.Errorf("persisting remote state: %w", err)
		}
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
