package cloud

import "context"

// ASGManager exposes Auto Scaling Group lifecycle data that the Cloud Control
// ResourceClient cannot provide: instance counts (InService, Pending, etc.)
// and per-instance lifecycle state. Cloud Control returns static configuration
// (DesiredCapacity, MinSize, MaxSize) but not dynamic runtime data.
//
// Same auxiliary-interface pattern as GameLiftManager and CodeBuildRunner;
// reached via type assertion on the Provider.
type ASGManager interface {
	// DescribeASG returns the live state of an Auto Scaling Group.
	DescribeASG(ctx context.Context, asgName string) (ASGInfo, error)
}

// ASGInfo is the provider-agnostic snapshot of an Auto Scaling Group.
type ASGInfo struct {
	// Name is the ASG name.
	Name string
	// DesiredCapacity is the target number of instances.
	DesiredCapacity int
	// MinSize is the minimum number of instances.
	MinSize int
	// MaxSize is the maximum number of instances.
	MaxSize int
	// InService is the number of instances that have passed health checks.
	InService int
	// Pending is the number of instances still launching.
	Pending int
	// Terminating is the number of instances being removed.
	Terminating int
}
