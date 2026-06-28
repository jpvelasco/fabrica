package cloud

import "context"

// CodeBuildRunner manages CodeBuild projects and builds through the CodeBuild
// SDK — the same auxiliary-interface pattern as EC2InstanceManager and
// StateBackendBootstrapper. CodeBuild project create/delete is here (not on the
// Cloud Control ResourceClient) because AWS::CodeBuild::Project does not support
// the Cloud Control CREATE action.
type CodeBuildRunner interface {
	// EnsureProject creates the CodeBuild project if it does not exist. created
	// is false when the project already existed (idempotent no-op).
	EnsureProject(ctx context.Context, spec CodeBuildProjectSpec) (created bool, err error)
	// DeleteProject deletes the named project; a missing project is not an error.
	DeleteProject(ctx context.Context, name string) error
	// StartBuild starts a build of the named project with the given environment
	// variable overrides, returning the build ID.
	StartBuild(ctx context.Context, project string, env map[string]string) (buildID string, err error)
	// BuildStatus returns the current status of a build.
	BuildStatus(ctx context.Context, buildID string) (BuildInfo, error)
	// BuildLog returns the CloudWatch log output for a build.
	BuildLog(ctx context.Context, buildID string) (string, error)
}

// CodeBuildProjectSpec is the provider-agnostic description of a CodeBuild
// project to create. The ci plan layer builds it; the AWS provider translates
// it into the CodeBuild SDK shape.
type CodeBuildProjectSpec struct {
	Name           string
	ServiceRoleARN string
	ComputeType    string
	Image          string
	BuildTimeout   int
	Buildspec      string
	EnvDefaults    map[string]string
	Tags           map[string]string
}

// BuildInfo is the provider-agnostic snapshot of a CodeBuild build.
type BuildInfo struct {
	ID        string
	Status    string // e.g. IN_PROGRESS, SUCCEEDED, FAILED, STOPPED
	Phase     string // current build phase, e.g. BUILD, POST_BUILD, COMPLETED
	LogGroup  string
	LogStream string
}
