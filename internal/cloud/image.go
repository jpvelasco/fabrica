package cloud

import "context"

// ImageInfo is the subset of EC2 image metadata Fabrica uses to reject
// AMIs that cannot satisfy a module contract (for example a stock Ubuntu
// image passed to workstation create).
type ImageInfo struct {
	ID          string
	Name        string
	Description string
	Owner       string
}

// ImageInspector returns metadata for a specific AMI. Optional: providers
// that do not implement it skip create-time AMI contract checks (fakes/E2E).
type ImageInspector interface {
	DescribeImage(ctx context.Context, imageID string) (ImageInfo, error)
}
