package cloud

import "context"

// CodeBuildRunner performs runtime operations on a CodeBuild project that are
// not available through the Cloud Control API (start a build, query its status,
// fetch its logs) — the same auxiliary-interface pattern as EC2InstanceManager.
type CodeBuildRunner interface {
	// StartBuild starts a build of the named project with the given environment
	// variable overrides, returning the build ID.
	StartBuild(ctx context.Context, project string, env map[string]string) (buildID string, err error)
	// BuildStatus returns the current status of a build.
	BuildStatus(ctx context.Context, buildID string) (BuildInfo, error)
	// BuildLog returns the CloudWatch log output for a build.
	BuildLog(ctx context.Context, buildID string) (string, error)
}

// BuildInfo is the provider-agnostic snapshot of a CodeBuild build.
type BuildInfo struct {
	ID        string
	Status    string // e.g. IN_PROGRESS, SUCCEEDED, FAILED, STOPPED
	Phase     string // current build phase, e.g. BUILD, POST_BUILD, COMPLETED
	LogGroup  string
	LogStream string
}
