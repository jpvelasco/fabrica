package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.ResourceClient = (*resourceClients)(nil)

// Create provisions a new cloud resource and blocks until the operation reaches
// a terminal state. Blocking keeps callers simple and consistent with existing
// perforce/horde create commands that immediately use r.Identifier after this call.
func (c *resourceClients) Create(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	r.DesiredState = injectFabricaTags(r.DesiredState, "fabrica", c.version, nil)

	out, err := c.cc.CreateResource(ctx, &cloudcontrol.CreateResourceInput{
		TypeName:     aws.String(r.TypeName),
		DesiredState: aws.String(string(r.DesiredState)),
	})
	if err != nil {
		return fmt.Errorf("creating %s: %w", r.TypeName, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s creation: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		return progressEventError(r.TypeName, result.ProgressEvent)
	}

	r.Identifier = aws.ToString(result.ProgressEvent.Identifier)
	return nil
}

// Get retrieves the current state of a resource and populates r.ActualState.
func (c *resourceClients) Get(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.GetResource(ctx, &cloudcontrol.GetResourceInput{
		TypeName:   aws.String(r.TypeName),
		Identifier: aws.String(r.Identifier),
	})
	if err != nil {
		if isNotFound(err) {
			return fabricac.ErrResourceNotFound
		}
		return fmt.Errorf("getting %s %s: %w", r.TypeName, r.Identifier, err)
	}

	if out.ResourceDescription != nil && out.ResourceDescription.Properties != nil {
		r.ActualState = json.RawMessage(*out.ResourceDescription.Properties)
	}
	return nil
}

// Update applies a JSON patch document (r.DesiredState) to the resource and blocks
// until the operation completes. r.DesiredState must be a valid RFC 6902 patch document,
// e.g. [{"op":"replace","path":"/Foo","value":"bar"}].
func (c *resourceClients) Update(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.UpdateResource(ctx, &cloudcontrol.UpdateResourceInput{
		TypeName:      aws.String(r.TypeName),
		Identifier:    aws.String(r.Identifier),
		PatchDocument: aws.String(string(r.DesiredState)),
	})
	if err != nil {
		return fmt.Errorf("updating %s %s: %w", r.TypeName, r.Identifier, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s update: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		return progressEventError(r.TypeName, result.ProgressEvent)
	}
	return nil
}

// Delete removes a resource and blocks until the operation completes.
// Returns cloud.ErrResourceNotFound if the resource does not exist (idempotent).
func (c *resourceClients) Delete(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.DeleteResource(ctx, &cloudcontrol.DeleteResourceInput{
		TypeName:   aws.String(r.TypeName),
		Identifier: aws.String(r.Identifier),
	})
	if err != nil {
		if isNotFound(err) {
			return fabricac.ErrResourceNotFound
		}
		return fmt.Errorf("deleting %s %s: %w", r.TypeName, r.Identifier, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s deletion: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		if result.ProgressEvent.ErrorCode == types.HandlerErrorCodeNotFound ||
			result.ProgressEvent.ErrorCode == types.HandlerErrorCodeAlreadyExists {
			return fabricac.ErrResourceNotFound
		}
		return progressEventError(r.TypeName, result.ProgressEvent)
	}
	return nil
}

// List returns all resources of the given type, paginating automatically.
func (c *resourceClients) List(ctx context.Context, typeName string) ([]fabricac.Resource, error) {
	if err := c.ensureClient(ctx); err != nil {
		return nil, err
	}

	var resources []fabricac.Resource
	var nextToken *string

	for {
		out, err := c.cc.ListResources(ctx, &cloudcontrol.ListResourcesInput{
			TypeName:  aws.String(typeName),
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", typeName, err)
		}

		for _, desc := range out.ResourceDescriptions {
			r := fabricac.Resource{
				TypeName:   typeName,
				Identifier: aws.ToString(desc.Identifier),
			}
			if desc.Properties != nil {
				r.ActualState = json.RawMessage(*desc.Properties)
			}
			resources = append(resources, r)
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return resources, nil
}

// ensureClient lazily initialises the SDK client and waiter on first use.
func (c *resourceClients) ensureClient(ctx context.Context) error {
	if c.cc != nil {
		return nil
	}

	loadCfg := c.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, c.awsCfg.region, c.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for Cloud Control: %w", err)
	}

	newClient := c.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ccAPIClient {
			return cloudcontrol.NewFromConfig(cfg)
		}
	}
	c.cc = newClient(cfg)

	newWaiter := c.newWaiter
	if newWaiter == nil {
		newWaiter = func(cl ccAPIClient) ccWaiter {
			return cloudcontrol.NewResourceRequestSuccessWaiter(cl.(cloudcontrol.GetResourceRequestStatusAPIClient))
		}
	}
	c.waiter = newWaiter(c.cc)

	return nil
}

func (c *resourceClients) timeout() time.Duration {
	if c.waitTimeout > 0 {
		return c.waitTimeout
	}
	return defaultWaitTimeout
}

// progressEventError builds an error from a FAILED ProgressEvent, including the
// StatusMessage when available so operators can see the provider's failure reason.
func progressEventError(typeName string, ev *types.ProgressEvent) error {
	msg := ""
	if ev.StatusMessage != nil && *ev.StatusMessage != "" {
		msg = ": " + *ev.StatusMessage
	}
	return fmt.Errorf("resource operation on %s failed (code: %s)%s", typeName, ev.ErrorCode, msg)
}

// isNotFound reports whether an SDK error represents a resource-not-found condition.
func isNotFound(err error) bool {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == string(types.HandlerErrorCodeNotFound) ||
			code == "NotFound" ||
			code == "ResourceNotFoundException"
	}
	return false
}
