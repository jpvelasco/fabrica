package cloud

import "context"

// VolumeTagger applies tags to the EBS volumes attached to an instance.
// Cloud Control cannot tag BlockDeviceMapping volumes at creation (instance-tag
// propagation does not apply), so create flows call this right after the
// instance step; without it, data volumes are invisible to ManagedBy sweeps.
type VolumeTagger interface {
	TagInstanceVolumes(ctx context.Context, instanceID string, tags map[string]string) error
}
