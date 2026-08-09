// Package drift (remediate.go) — apply engine for drift auto-remediation.
//
// This file contains the ApplyRemediation function that executes a
// RemediationPlan against live AWS via the provider's ResourceClient.
// The create seam allows tests to inject fakes.
//
// V1 scope: recreate Missing EC2 instances and security groups. Mismatch
// and Extra are report-only.

package drift

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

// ApplyRemediation executes a RemediationPlan against the live provider.
// Only ActionCreate items are applied; ActionSkip items are collected as
// skipped. Partial failure stops on the first error and returns what was
// applied, skipped, and failed so far.
func ApplyRemediation(ctx context.Context, plan *RemediationPlan, st *state.State, createResource func(ctx context.Context, r *cloud.Resource) error) *RemediationResult {
	result := &RemediationResult{}

	for i := range plan.Actions {
		a := &plan.Actions[i]

		if a.Kind != ActionCreate {
			result.Skipped = append(result.Skipped, *a)
			continue
		}

		// Update state before the create so partial failures are recoverable.
		// The create seam is called first; state is updated on success.
		if createResource == nil {
			result.Failed = append(result.Failed, *a)
			result.Errors = append(result.Errors, fmt.Sprintf("%s/%s: no create seam available", a.Module, a.TypeName))
			continue
		}
		if a.Resource == nil || a.Resource.DesiredState == nil {
			result.Failed = append(result.Failed, *a)
			result.Errors = append(result.Errors, fmt.Sprintf("%s/%s: no desired state available for recreation", a.Module, a.TypeName))
			continue
		}

		err := createResource(ctx, a.Resource)
		if err != nil {
			result.Failed = append(result.Failed, *a)
			result.Errors = append(result.Errors, fmt.Sprintf("%s/%s: %v", a.Module, a.TypeName, err))
			// Stop on first error — remaining actions are not attempted.
			// Collect them as skipped so the caller sees the full picture.
			for j := i + 1; j < len(plan.Actions); j++ {
				ra := plan.Actions[j]
				ra.Reason = "skipped due to prior failure"
				result.Skipped = append(result.Skipped, ra)
			}
			return result
		}

		// Update state: mark the module as ready after successful create.
		// The provider's Create call sets Resource.Identifier to the new ID,
		// so sync it back into state so subsequent drift sees it as inSync.
		resources := resourcesForModule(st, a.Module)
		for k := range resources {
			if resources[k].TypeName == a.TypeName && resources[k].Identifier == a.Identifier {
				resources[k].Identifier = a.Resource.Identifier
				break
			}
		}
		if mod := st.GetModule(a.Module); mod != nil {
			st.UpsertModule(a.Module, mod.Version, "ready", resources)
		}

		// If we just created an SG, refresh the SecurityGroupIds in any
		// pending instance actions so they reference the new SG identifier.
		if a.TypeName == cloud.TypeAWSEC2SecurityGroup {
			refreshSGIDs(plan, i, a.Module, a.Resource.Identifier)
		}

		result.Applied = append(result.Applied, *a)
	}

	return result
}

// resourcesForModule returns the resource list for a module from state.
func resourcesForModule(st *state.State, name string) []state.ModuleResource {
	m := st.GetModule(name)
	if m == nil {
		return nil
	}
	return m.Resources
}

// refreshSGIDs updates the SecurityGroupIds in the DesiredState of any
// pending EC2 instance actions (after index idx) that belong to the same
// module as the SG that was just created. This ensures the instance references
// the new SG identifier instead of the old one.
func refreshSGIDs(plan *RemediationPlan, idx int, module string, newSGID string) {
	for i := idx + 1; i < len(plan.Actions); i++ {
		a := &plan.Actions[i]
		if a.Module != module || a.TypeName != cloud.TypeAWSEC2Instance || a.Resource == nil || a.Resource.DesiredState == nil {
			continue
		}
		var ds map[string]any
		if err := json.Unmarshal(a.Resource.DesiredState, &ds); err != nil {
			continue
		}
		ds["SecurityGroupIds"] = []string{newSGID}
		if b, err := json.Marshal(ds); err == nil {
			a.Resource.DesiredState = b
		}
	}
}
