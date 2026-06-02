package cloud

import "context"

// EC2InstanceManager performs lifecycle operations on EC2 instances that are not
// available through the Cloud Control API (stop, start).
type EC2InstanceManager interface {
	StopInstance(ctx context.Context, instanceID string) error
	StartInstance(ctx context.Context, instanceID string) error
}
