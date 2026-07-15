package perforce

import (
	"context"
	"fmt"
	"regexp"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

var (
	reVersionMinor = regexp.MustCompile(`^\d{4}\.\d+$`)
	reVersionBuild = regexp.MustCompile(`^\d{4}\.\d+/\d+$`)
)

// CreatePlan holds everything needed to provision Perforce: resolved names,
// resource specs, cost inputs. No AWS SDK types — callers execute the plan.
type CreatePlan struct {
	Account      string
	Region       string
	InstanceType string
	HelixVersion string
	VolumeSize   int
	VPCID        string
	SubnetID     string
	DefaultVPC   bool

	SGName              string
	InstanceName        string
	RoleName            string
	InstanceProfileName string

	// Backup settings copied from config for IAM S3 policy (optional).
	BackupS3Export bool
	BackupS3Bucket string
	BackupS3Prefix string

	CostResources []cost.Resource
}

// NewCreatePlan validates inputs and builds a CreatePlan. VPCResolver is called
// only when VPCId/SubnetId are absent from cfg; pass nil to skip resolution
// (dry-run with explicit VPC values, or tests).
func NewCreatePlan(ctx context.Context, cfg config.PerforceConfig, account, region, version string, resolver cloud.VPCResolver) (*CreatePlan, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m5.xlarge"
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 500
	}

	vpcID := cfg.VPCId
	subnetID := cfg.SubnetId
	defaultVPC := false

	if (vpcID == "" || subnetID == "") && resolver != nil {
		var err error
		vpcID, subnetID, err = resolver.ResolveDefaultVPC(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolving default VPC: %w", err)
		}
		defaultVPC = true
	}

	s3Prefix := ResolveS3Prefix(cfg.Backup.S3Prefix)
	return &CreatePlan{
		Account:             account,
		Region:              region,
		InstanceType:        instanceType,
		HelixVersion:        version,
		VolumeSize:          volumeSize,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		SGName:              "fabrica-perforce-sg",
		InstanceName:        "fabrica-perforce",
		RoleName:            "fabrica-perforce-role",
		InstanceProfileName: "fabrica-perforce-profile",
		BackupS3Export:      cfg.Backup.S3Export,
		BackupS3Bucket:      cfg.Backup.S3Bucket,
		BackupS3Prefix:      s3Prefix,
		CostResources:       CostResources(cfg),
	}, nil
}

// ResolveVersion returns the effective Helix Core version using the precedence:
// CLI flag > config file > built-in default.
func ResolveVersion(flagValue, cfgValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if cfgValue != "" {
		return cfgValue
	}
	return DefaultHelixVersion
}

// validateVersion returns an error if v is not "latest", "YYYY.N", or "YYYY.N/BUILD".
func validateVersion(v string) error {
	if v == "latest" || reVersionMinor.MatchString(v) || reVersionBuild.MatchString(v) {
		return nil
	}
	return fmt.Errorf("invalid Helix Core version %q: must be \"latest\", \"YYYY.N\", or \"YYYY.N/BUILD\"", v)
}
