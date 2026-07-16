package cloud

import "context"

// RemoteRunner runs shell commands on an EC2 instance (SSM Run Command on AWS).
// Same auxiliary-interface pattern as EC2InstanceManager and CodeBuildRunner —
// Cloud Control cannot execute remote commands.
type RemoteRunner interface {
	RunCommand(ctx context.Context, instanceID string, commands []string) (RemoteResult, error)
}

// RemoteResult is the outcome of a remote shell invocation.
type RemoteResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}
