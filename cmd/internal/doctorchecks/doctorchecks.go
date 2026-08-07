// Package doctorchecks provides shared diagnostic checks used by both the
// CLI doctor command and the MCP doctor tool. It is importable from cmd/mcp
// without introducing a dependency on cmd/doctor.
package doctorchecks

import (
	"context"
	"runtime"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
)

// DoctorCheck is the result of one diagnostic check.
type DoctorCheck struct {
	Name    string
	Status  string // "ok", "warning", "fail"
	Message string
}

// RunChecks runs all diagnostic checks and returns the results.
// It degrades cleanly when backend is nil (returns warnings, not errors).
func RunChecks(ctx context.Context, rt globals.Runtime, backend cloud.StateBackendChecker) []DoctorCheck {
	return []DoctorCheck{
		checkGo(),
		checkVersion(),
		checkCreds(ctx, rt),
		checkRegion(rt),
		checkBucket(ctx, rt, backend),
		checkTable(ctx, rt, backend),
	}
}

func checkGo() DoctorCheck {
	return DoctorCheck{"Go version", "ok", runtime.Version()}
}

func checkVersion() DoctorCheck {
	msg := fabricav.Version
	if fabricav.Commit != "" {
		msg += " (commit " + fabricav.Commit + ")"
	}
	return DoctorCheck{"Fabrica version", "ok", msg}
}

func checkCreds(ctx context.Context, rt globals.Runtime) DoctorCheck {
	if rt.Provider == nil {
		return DoctorCheck{"AWS credentials", "warning", "no provider configured"}
	}
	_, _, _, err := rt.Provider.Identity(ctx)
	if err != nil {
		return DoctorCheck{"AWS credentials", "fail", "could not authenticate — check your credentials and region"}
	}
	return DoctorCheck{"AWS credentials", "ok", "authenticated"}
}

func checkRegion(rt globals.Runtime) DoctorCheck {
	if rt.Config == nil || rt.Config.Cloud.AWS.Region == "" {
		return DoctorCheck{"Region", "warning", "not set — using " + config.DefaultAWSRegion + " default"}
	}
	return DoctorCheck{"Region", "ok", rt.Config.Cloud.AWS.Region}
}

func checkBucket(ctx context.Context, rt globals.Runtime, backend cloud.StateBackendChecker) DoctorCheck {
	if rt.Config == nil {
		return stateBackendWarning("S3 state bucket")
	}
	bucket := rt.Config.State.Bucket
	if bucket == "" {
		return stateBackendWarning("S3 state bucket")
	}
	if backend == nil {
		return DoctorCheck{"S3 state bucket", "warning", "state backend checker unavailable for provider"}
	}
	ok, err := backend.StateBucketExists(ctx, bucket)
	if err != nil {
		return DoctorCheck{"S3 state bucket", "fail", "check failed: " + err.Error()}
	}
	if ok {
		return DoctorCheck{"S3 state bucket", "ok", bucket}
	}
	return DoctorCheck{"S3 state bucket", "warning", "bucket not found (run fabrica setup)"}
}

func checkTable(ctx context.Context, rt globals.Runtime, backend cloud.StateBackendChecker) DoctorCheck {
	if rt.Config == nil {
		return stateBackendWarning("DynamoDB lock table")
	}
	bucket := rt.Config.State.Bucket
	table := rt.Config.State.Table
	if bucket == "" {
		return stateBackendWarning("DynamoDB lock table")
	}
	if backend == nil {
		return DoctorCheck{"DynamoDB lock table", "warning", "state backend checker unavailable for provider"}
	}
	ok, err := backend.StateLockTableExists(ctx, table)
	if err != nil {
		return DoctorCheck{"DynamoDB lock table", "fail", "check failed: " + err.Error()}
	}
	if ok {
		return DoctorCheck{"DynamoDB lock table", "ok", table}
	}
	return DoctorCheck{"DynamoDB lock table", "warning", "table not found (run fabrica setup)"}
}

func stateBackendWarning(name string) DoctorCheck {
	return DoctorCheck{name, "warning", "not yet provisioned (run fabrica setup)"}
}
