package aws

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// ccAPIClient is the subset of the Cloud Control SDK client surface used by resourceClients.
type ccAPIClient interface {
	CreateResource(ctx context.Context, params *cloudcontrol.CreateResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.CreateResourceOutput, error)
	GetResource(ctx context.Context, params *cloudcontrol.GetResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)
	UpdateResource(ctx context.Context, params *cloudcontrol.UpdateResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.UpdateResourceOutput, error)
	DeleteResource(ctx context.Context, params *cloudcontrol.DeleteResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.DeleteResourceOutput, error)
	ListResources(ctx context.Context, params *cloudcontrol.ListResourcesInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error)
	GetResourceRequestStatus(ctx context.Context, params *cloudcontrol.GetResourceRequestStatusInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}

// ccWaiter polls GetResourceRequestStatus until a resource operation reaches SUCCESS or FAILED.
// WaitForOutput is used so callers can read ProgressEvent.Identifier from the result
// without an extra GetResourceRequestStatus call.
type ccWaiter interface {
	WaitForOutput(ctx context.Context, params *cloudcontrol.GetResourceRequestStatusInput, maxWait time.Duration, optFns ...func(*cloudcontrol.ResourceRequestSuccessWaiterOptions)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}
