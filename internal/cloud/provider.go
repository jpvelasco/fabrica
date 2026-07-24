// Package cloud defines the provider-agnostic interfaces that all Fabrica
// cloud providers must implement. No cloud SDK imports belong here.
package cloud

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrResourceNotFound is returned by ResourceClient methods when the target
// resource does not exist in the provider (already deleted or never created).
var ErrResourceNotFound = errors.New("resource not found")

// ErrNotHandled is returned by SDKDeleteFunc when the resource type is not
// managed by the SDK path and should fall back to Cloud Control deletion.
var ErrNotHandled = errors.New("not handled by SDK — fall back to Cloud Control")

// Provider is the interface every cloud provider must satisfy.
type Provider interface {
	// Name returns the registered provider identifier (e.g. "aws").
	Name() string

	// Identity returns the authenticated account information.
	Identity(ctx context.Context) (account, arn, region string, err error)

	// Resources returns the resource management client for this provider.
	Resources() ResourceClient
}

// ResourceClient manages cloud resources through a uniform CRUD interface.
type ResourceClient interface {
	Create(ctx context.Context, r *Resource) error
	Get(ctx context.Context, r *Resource) error
	Update(ctx context.Context, r *Resource) error
	Delete(ctx context.Context, r *Resource) error
	List(ctx context.Context, typeName string) ([]Resource, error)
}

// Resource describes a cloud resource in a provider-agnostic form.
type Resource struct {
	TypeName     string          `json:"typeName"`
	Identifier   string          `json:"identifier"`
	DesiredState json.RawMessage `json:"desiredState,omitempty"`
	ActualState  json.RawMessage `json:"actualState,omitempty"`
}
