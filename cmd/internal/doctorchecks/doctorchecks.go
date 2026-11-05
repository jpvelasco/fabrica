// Package doctorchecks provides shared diagnostic checks used by both the
// CLI doctor command and the MCP doctor tool. It is importable from cmd/mcp
// without introducing a dependency on cmd/doctor.
package doctorchecks

import (
	"context"
	"fmt"
	"net"
	"runtime"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/perforce"
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
		checkPerforceCIDR(rt),
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
		msg := "could not authenticate — check cloud.aws.profile / AWS_PROFILE and region"
		if cloud.IsExpiredSSOSession(err.Error()) {
			msg = "SSO session expired or invalid — run 'aws sso login' for the configured profile and retry"
		}
		return DoctorCheck{"AWS credentials", "fail", msg}
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

// perforceSectionConfigured reports whether the operator set any perforce.*
// key, so the CIDR check runs for a configured deployment even when
// instanceType is left at the default.
func perforceSectionConfigured(p config.PerforceConfig) bool {
	if p.Version != "" || p.InstanceType != "" || p.VPCId != "" || p.SubnetId != "" || p.AllowedCIDR != "" {
		return true
	}
	if p.VolumeSize > 0 {
		return true
	}
	b := p.Backup
	if b.Path != "" || b.S3Export || b.S3Bucket != "" || b.S3Prefix != "" || b.Schedule != "" || b.Retain > 0 {
		return true
	}
	return p.IAM.Path != "" || p.IAM.PermissionsBoundary != ""
}

// checkPerforceCIDR checks perforce.allowedCidr when perforce is configured.
// Unset warns: perforce falls back to 10.0.0.0/8, which does not cover AWS
// default VPCs (172.31.0.0/16), so in-VPC clients would not reach port 1666.
// A set value must parse as a CIDR (fail otherwise) and must not be a /0
// prefix, which opens port 1666 to the internet (warn).
func checkPerforceCIDR(rt globals.Runtime) DoctorCheck {
	if rt.Config == nil || !perforceSectionConfigured(rt.Config.Perforce) {
		return DoctorCheck{"Perforce CIDR", "ok", "not configured"}
	}
	cidr := rt.Config.Perforce.AllowedCIDR
	if cidr == "" {
		return DoctorCheck{"Perforce CIDR", "warning", fmt.Sprintf("unset — port 1666 opens only to %s (default). perforce does not auto-resolve the VPC CIDR. Set perforce.allowedCidr to your VPC CIDR (e.g. 172.31.0.0/16 on the default VPC)", perforce.DefaultAllowedCIDR)}
	}
	return classifyPerforceCIDR(cidr)
}

// classifyPerforceCIDR grades a configured perforce.allowedCidr.
func classifyPerforceCIDR(cidr string) DoctorCheck {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return DoctorCheck{"Perforce CIDR", "fail", fmt.Sprintf("%s is not a valid CIDR (%v). Set perforce.allowedCidr to your VPC CIDR, e.g. 172.31.0.0/16", cidr, err)}
	}
	if ones, _ := ipnet.Mask.Size(); ones == 0 {
		return DoctorCheck{"Perforce CIDR", "warning", cidr + " opens port 1666 to the entire internet. Restrict perforce.allowedCidr to your VPC CIDR, e.g. 172.31.0.0/16"}
	}
	return DoctorCheck{"Perforce CIDR", "ok", cidr + " (configured)"}
}
