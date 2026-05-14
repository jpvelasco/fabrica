package aws

import (
	"context"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

var _ cloud.ResourceClient = (*resourceClients)(nil)

func (c *resourceClients) Create(ctx context.Context, r *cloud.Resource) error {
	r.DesiredState = injectFabricaTags(r.DesiredState, "fabrica", "0.0.0", nil)
	// TODO: implement actual Cloud Control API CreateResource call
	_ = c.cc
	_ = ctx
	return nil
}

func (c *resourceClients) Get(ctx context.Context, r *cloud.Resource) error {
	// TODO: implement actual Cloud Control API GetResource call
	_ = c.cc
	_ = ctx
	return nil
}

func (c *resourceClients) Update(ctx context.Context, r *cloud.Resource) error {
	// TODO: implement actual Cloud Control API UpdateResource call
	_ = c.cc
	_ = ctx
	return nil
}

func (c *resourceClients) Delete(ctx context.Context, r *cloud.Resource) error {
	// TODO: implement actual Cloud Control API DeleteResource call
	_ = c.cc
	_ = ctx
	return nil
}

func (c *resourceClients) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	// TODO: implement actual Cloud Control API ListResources call
	_ = c.cc
	_ = ctx
	return nil, nil
}
