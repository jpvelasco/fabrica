package state

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// BackendSetupApplyPlan owns the resolved target, approved snapshots and exact
// desired tags for preview and apply. Apply revalidates both peers before writes.
type BackendSetupApplyPlan struct {
	Target                SetupPlan
	Identity              tags.Identity
	Ownership             BackendOwnershipPlan
	BucketTags, TableTags map[string]string
}

// PlanBackendSetup composes protected tags from an approved ownership plan.
// It does not classify ownership, read state, or authorize adoption.
func PlanBackendSetup(target SetupPlan, identity tags.Identity, ownership BackendOwnershipPlan, version string, operatorTags map[string]string) (BackendSetupApplyPlan, error) {
	plan := BackendSetupApplyPlan{Target: target, Identity: identity, Ownership: ownership}
	plan.Target.Resources = slices.Clone(target.Resources)
	for _, component := range []struct {
		decision *BackendComponentDecision
		desired  *map[string]string
	}{{&plan.Ownership.Bucket, &plan.BucketTags}, {&plan.Ownership.Table, &plan.TableTags}} {
		component.decision.Observed.Tags = maps.Clone(component.decision.Observed.Tags)
		base := identity.Standard("setup", version)
		if marker, present := backendSetupMarker(identity, *component.decision); present {
			base[tags.OwnershipVersionKey] = marker
		}
		*component.desired = identity.MergeProtected(base, operatorTags)
	}
	if err := validateBackendSetupPlan(plan); err != nil {
		return BackendSetupApplyPlan{}, err
	}
	return plan, nil
}

func backendSetupMarker(identity tags.Identity, decision BackendComponentDecision) (string, bool) {
	if decision.Action == BackendComponentCreate {
		return "1", identity.ManagedBy() != tags.ManagedByValue
	}
	marker, present := decision.Observed.Tags[tags.OwnershipVersionKey]
	return marker, present
}

// Both planning and applying enforce these shape/tag invariants. Ownership
// policy remains in C1; a mutable plan cannot select another desired owner.
func validateBackendSetupPlan(plan BackendSetupApplyPlan) error {
	target := plan.Target
	if target.Account == "" || target.Region == "" || target.Backend.Bucket == "" || target.Backend.Table == "" {
		return fmt.Errorf("setup target is unresolved; resolve the account, region and backend names before planning")
	}
	for _, component := range []struct {
		name     string
		decision BackendComponentDecision
		desired  map[string]string
	}{{"bucket", plan.Ownership.Bucket, plan.BucketTags}, {"table", plan.Ownership.Table, plan.TableTags}} {
		switch component.decision.Action {
		case BackendComponentCreate:
			if component.decision.Observed.Exists || len(component.decision.Observed.Tags) != 0 {
				return fmt.Errorf("setup %s create plan is inconsistent with its snapshot; repeat the full preflight", component.name)
			}
		case BackendComponentReconcile, BackendComponentRepairLegacy:
			if !component.decision.Observed.Exists {
				return fmt.Errorf("setup %s existing-resource plan has an absent snapshot; repeat the full preflight", component.name)
			}
		default:
			return fmt.Errorf("setup %s action is unapproved; repeat the full preflight", component.name)
		}
		version := component.desired["FabricaVersion"]
		if version == "" {
			return fmt.Errorf("setup %s version tag is empty; rebuild the protected setup plan", component.name)
		}
		for key, value := range plan.Identity.Standard("setup", version) {
			if component.desired[key] != value {
				return fmt.Errorf("setup %s protected tag %s changed; rebuild the protected setup plan", component.name, key)
			}
		}
		want, expected := backendSetupMarker(plan.Identity, component.decision)
		actual, present := component.desired[tags.OwnershipVersionKey]
		if present != expected || actual != want && expected {
			return fmt.Errorf("setup %s ownership marker changed; rebuild the protected setup plan", component.name)
		}
	}
	return nil
}

// ApplyBackendSetup requires fresh direct canonical evidence and stable approval
// for both peers before changing either. Component adapters recheck their final
// observations; S3 creation still has a nontransactional read/create gap.
// Returned results include successful create requests on later phase failure.
// The caller owns confirmation, locking, persistence and configuration.
func ApplyBackendSetup(ctx context.Context, provider cloud.Provider, plan BackendSetupApplyPlan) ([]BootstrapResult, error) {
	if err := validateBackendSetupPlan(plan); err != nil {
		return nil, err
	}
	creator, canCreate := provider.(cloud.StateBackendCreator)
	reconciler, canReconcile := provider.(cloud.StateBackendReconciler)
	for _, decision := range []BackendComponentDecision{plan.Ownership.Bucket, plan.Ownership.Table} {
		if decision.Action == BackendComponentCreate && !canCreate {
			return nil, fmt.Errorf("provider cannot create state backend components; use a provider with guarded creation before setup")
		}
		if decision.Action != BackendComponentCreate && !canReconcile {
			return nil, fmt.Errorf("provider cannot reconcile state backend components; use a provider with guarded reconciliation before setup")
		}
	}
	_, fresh, err := InspectBackendOwnership(ctx, provider, plan.Target, plan.Identity)
	if err != nil {
		return nil, fmt.Errorf("revalidating setup backend: %w", err)
	}
	for _, peer := range [][2]BackendComponentDecision{{plan.Ownership.Bucket, fresh.Bucket}, {plan.Ownership.Table, fresh.Table}} {
		if peer[0].Action != peer[1].Action || peer[0].Observed.Exists != peer[1].Observed.Exists {
			return nil, fmt.Errorf("backend action changed since preview; repeat the full setup preflight")
		}
		for _, key := range []string{tags.ManagedByKey, tags.OwnershipVersionKey} {
			before, beforePresent := peer[0].Observed.Tags[key]
			after, afterPresent := peer[1].Observed.Tags[key]
			if beforePresent != afterPresent || before != after {
				return nil, fmt.Errorf("protected backend tag %s changed since preview; repeat the full setup preflight", key)
			}
		}
	}
	var results []BootstrapResult
	for i, component := range []struct {
		name, label string
		decision    BackendComponentDecision
		desired     map[string]string
	}{{plan.Target.Backend.Bucket, "S3 bucket", fresh.Bucket, plan.BucketTags}, {plan.Target.Backend.Table, "DynamoDB table", fresh.Table, plan.TableTags}} {
		desired := maps.Clone(component.desired)
		if component.decision.Action == BackendComponentCreate {
			var created cloud.StateBackendCreateResult
			if i == 0 {
				created, err = creator.CreateStateBucket(ctx, component.name, cloud.StateBucketCreateSpec{Region: plan.Target.Region, StateDocumentKey: StateDocumentKey, Tags: desired})
			} else {
				created, err = creator.CreateStateLockTable(ctx, component.name, desired)
			}
			if created.Created {
				results = append(results, BootstrapResult{Name: component.label + " " + component.name})
			} else if err == nil {
				err = fmt.Errorf("create did not create the expected-absent resource; repeat the full setup preflight")
			}
		} else {
			spec := cloud.StateBackendReconcileSpec{Expected: component.decision.Observed, Tags: desired}
			spec.Expected.Tags = maps.Clone(spec.Expected.Tags)
			if i == 0 {
				err = reconciler.ReconcileStateBucket(ctx, component.name, spec)
			} else {
				err = reconciler.ReconcileStateLockTable(ctx, component.name, spec)
			}
			if err == nil {
				results = append(results, BootstrapResult{Name: component.label + " " + component.name, Existed: true})
			}
		}
		if err != nil {
			return results, fmt.Errorf("applying %s %s (%d components handled): %w", component.label, component.name, len(results), err)
		}
	}
	return results, nil
}
