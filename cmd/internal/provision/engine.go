package provision

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// CreateStep describes one step in a create flow: build a desired state,
// create the resource, and persist state.
type CreateStep struct {
	// Label is the human-readable label for this step (e.g. "Security group").
	Label string
	// TypeName is the Cloud Control resource type (e.g. "AWS::EC2::SecurityGroup").
	TypeName string
	// BuildDesiredState builds the Cloud Control desired-state JSON blob.
	BuildDesiredState func() ([]byte, error)
	// ResourceIdentifier extracts the resource identifier from the created resource.
	// If nil, uses the Identifier field set by the provider.
	ResourceIdentifier func(created *cloud.Resource) string
	// Properties are optional properties to store in the module resource state.
	Properties map[string]string
	// IgnoreWriteError, when true, causes writeState failures to be silently
	// ignored after this step. Defaults to false (fail on writeState error).
	IgnoreWriteError bool
}

// ExecuteStep runs one create step: build desired state → create → print →
// append resource → upsert module → write state.
//
// This is the core micro-pattern repeated across all module create commands.
func ExecuteStep(
	ctx context.Context,
	step CreateStep,
	moduleName, version, status string,
	resources []fabricastate.ModuleResource,
	st *fabricastate.State,
	out io.Writer,
	createResource func(ctx context.Context, r *cloud.Resource) error,
	writeState func(*fabricastate.State) error,
) ([]fabricastate.ModuleResource, error) {
	desiredState, err := step.BuildDesiredState()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", step.Label, err)
	}

	res := &cloud.Resource{
		TypeName:     step.TypeName,
		DesiredState: desiredState,
	}
	if err := createResource(ctx, res); err != nil {
		return nil, fmt.Errorf("%s: %w", step.Label, err)
	}

	identifier := res.Identifier
	if step.ResourceIdentifier != nil {
		identifier = step.ResourceIdentifier(res)
	}
	fmt.Fprintf(out, "  %s created: %s\n", step.Label, identifier)

	moduleRes := fabricastate.ModuleResource{
		TypeName:   step.TypeName,
		Identifier: identifier,
	}
	if step.Properties != nil {
		moduleRes.Properties = step.Properties
	}
	resources = append(resources, moduleRes)
	st.UpsertModule(moduleName, version, status, resources)
	if err := writeState(st); err != nil {
		if !step.IgnoreWriteError {
			return nil, fmt.Errorf("writing state after %s: %w", step.Label, err)
		}
	}
	return resources, nil
}
