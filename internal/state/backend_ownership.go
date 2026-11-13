package state

import (
	"fmt"
	"maps"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// BackendOwnershipEvidence contains successful reads of the resolved backend.
// Canonical must be directly fetched remote ParseState evidence; nil means the
// remote document is confirmed absent. Local fallback is never canonical proof.
// Read/parse failures must be returned by the caller rather than passed here.
type BackendOwnershipEvidence struct {
	Canonical     *State
	Bucket, Table cloud.StateBackendMetadata
}

// BackendComponentAction describes an approved backend operation.
type BackendComponentAction uint8

const (
	// BackendComponentUnknown is unapproved, including the zero plan on failure.
	BackendComponentUnknown BackendComponentAction = iota
	BackendComponentCreate
	BackendComponentReconcile
	BackendComponentRepairLegacy
)

// BackendComponentDecision retains an owned snapshot for apply-time revalidation.
// RepairLegacy authorizes backend-only repair, never module-resource retagging.
type BackendComponentDecision struct {
	Action   BackendComponentAction
	Observed cloud.StateBackendMetadata
}

// BackendOwnershipPlan approves both components together, or neither on error.
type BackendOwnershipPlan struct {
	Bucket, Table BackendComponentDecision
}

// ClassifyBackendOwnership validates canonical state and actual backend tags.
// It performs no I/O, state binding, or mutation; setup must revalidate evidence
// before applying this plan. A fresh unbound State cannot provide remote proof.
func ClassifyBackendOwnership(identity tags.Identity, account, region string, evidence BackendOwnershipEvidence) (BackendOwnershipPlan, error) {
	canonicalOwner := ""
	if st := evidence.Canonical; st != nil {
		if err := ValidateIdentity(st, account, region); err != nil {
			return BackendOwnershipPlan{}, fmt.Errorf("canonical state target mismatch (recorded account %q and region %q vs configured account %q and region %q); restore the configuration that created this backend and inspect its state", st.Account, st.Region, account, region)
		}
		if err := st.ValidateOwnership(identity); err != nil {
			return BackendOwnershipPlan{}, fmt.Errorf("canonical backend state: %w", err)
		}
		if st.ManagedBy == "" {
			return BackendOwnershipPlan{}, fmt.Errorf("canonical state is unbound; read the directly persisted state document before approving backend ownership")
		}
		canonicalOwner = st.ManagedBy
	}
	bucket, err := classifyBackendComponent(identity, canonicalOwner, evidence.Bucket)
	if err != nil {
		return BackendOwnershipPlan{}, fmt.Errorf("state bucket: %w", err)
	}
	table, err := classifyBackendComponent(identity, canonicalOwner, evidence.Table)
	if err != nil {
		return BackendOwnershipPlan{}, fmt.Errorf("state lock table: %w", err)
	}
	return BackendOwnershipPlan{Bucket: bucket, Table: table}, nil
}

func classifyBackendComponent(identity tags.Identity, canonicalOwner string, metadata cloud.StateBackendMetadata) (BackendComponentDecision, error) {
	var action BackendComponentAction
	if !metadata.Exists {
		if len(metadata.Tags) != 0 {
			return BackendComponentDecision{}, fmt.Errorf("inconsistent absent-resource metadata contains tags; inspect the backend before retrying")
		}
		action = BackendComponentCreate
	} else {
		owner := metadata.Tags[tags.ManagedByKey]
		if owner == "" {
			return BackendComponentDecision{}, fmt.Errorf("ambiguous backend has no nonempty ManagedBy tag; inspect its ownership before retrying")
		}
		marker, marked := metadata.Tags[tags.OwnershipVersionKey]
		if marked && marker != "1" {
			return BackendComponentDecision{}, fmt.Errorf("unknown ownership format %q; inspect the backend with a compatible version before retrying", marker)
		}
		switch {
		case owner == identity.ManagedBy():
			if canonicalOwner == "" && owner != tags.ManagedByValue && !marked {
				return BackendComponentDecision{}, fmt.Errorf("custom owner %q has neither compatible canonical remote state nor an ownership format marker; inspect the backend before retrying", owner)
			}
			action = BackendComponentReconcile
		case canonicalOwner == tags.ManagedByValue && !marked:
			// Old operator tags allowed arbitrary nonempty ownership text.
			// Only verified default remote state permits this historical repair.
			action = BackendComponentRepairLegacy
		default:
			return BackendComponentDecision{}, fmt.Errorf("backend ownership mismatch (recorded %q vs configured %q); restore the configuration that created this backend and inspect its state", owner, identity.ManagedBy())
		}
	}
	return BackendComponentDecision{
		Action: action,
		Observed: cloud.StateBackendMetadata{
			Exists: metadata.Exists,
			Tags:   maps.Clone(metadata.Tags),
		},
	}, nil
}
