package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// InspectBackendOwnership reads actual metadata and the canonical document
// directly, then classifies them together. It never reads local fallback,
// refreshes a cache, acquires a lock, binds state, or changes backend resources.
// Returned evidence is approved only for preflight; apply must revalidate it.
func InspectBackendOwnership(ctx context.Context, provider cloud.Provider, setup SetupPlan, identity tags.Identity) (BackendOwnershipEvidence, BackendOwnershipPlan, error) {
	if provider == nil {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("no backend provider available; load the intended provider before setup")
	}
	reader, ok := provider.(cloud.StateBackendMetadataReader)
	if !ok {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("provider cannot inspect backend metadata; use a provider with state backend inspection before setup")
	}
	store, ok := provider.(cloud.StateStore)
	if !ok {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("provider cannot read the canonical state document; use a provider with state storage before setup")
	}
	if setup.Backend.Bucket == "" || setup.Backend.Table == "" {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("setup backend names are unresolved; resolve the target before backend inspection")
	}
	bucket, err := reader.StateBucketMetadata(ctx, setup.Backend.Bucket)
	if err != nil {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("reading setup bucket metadata: %w", err)
	}
	table, err := reader.StateLockTableMetadata(ctx, setup.Backend.Table)
	if err != nil {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("reading setup table metadata: %w", err)
	}
	evidence := BackendOwnershipEvidence{Bucket: bucket, Table: table}
	doc, err := store.GetStateDocument(ctx, setup.Backend.Bucket, StateDocumentKey)
	switch {
	case errors.Is(err, cloud.ErrStateDocumentMissing):
		// Only directly confirmed absence leaves Canonical nil. Local caches
		// cannot supply the proof required for historical backend repair.
	case err != nil:
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("reading canonical backend state: %w", err)
	default:
		// The classifier owns target/ownership validation and recovery guidance.
		evidence.Canonical, err = ParseState(doc, "", "")
		if err != nil {
			return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("reading canonical backend state: %w", err)
		}
	}
	plan, err := ClassifyBackendOwnership(identity, setup.Account, setup.Region, evidence)
	if err != nil {
		return BackendOwnershipEvidence{}, BackendOwnershipPlan{}, fmt.Errorf("inspecting backend ownership: %w", err)
	}
	return evidence, plan, nil
}
