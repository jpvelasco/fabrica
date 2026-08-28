package lore

import (
	"context"
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// Store backend kinds for lore.storeBackend.
const (
	StoreBackendLocal = "local"
	StoreBackendS3    = "s3"
)

// DefaultInstanceType is the EC2 shape used when config omits instanceType.
const DefaultInstanceType = "m5.xlarge"

// Default ports for loreserver (Epic Lore).
const (
	DefaultGRPCPort  = 41337 // gRPC over TCP and QUIC over UDP
	DefaultHTTPPort  = 41339 // HTTP health (GET /health_check)
	DefaultStorePath = "/opt/loreserver/store"
	DefaultConfigDir = "/etc/loreserver"
)

// CreatePlan holds everything needed to provision Lore: resolved names,
// resource specs, cost inputs. No AWS SDK types — callers execute the plan.
type CreatePlan struct {
	Account      string
	Region       string
	AmiID        string
	InstanceType string
	VolumeSize   int
	GRPCPort     int
	HTTPPort     int
	AllowedCIDR  string
	VPCID        string
	SubnetID     string
	DefaultVPC   bool

	SGName       string
	InstanceName string

	// S3 store (optional, enabled when storeBackend is "s3").
	StoreBackend        string
	StoreBucket         string
	StoreTables         []string
	RoleName            string
	InstanceProfileName string

	// TLS settings (optional).
	TLSConfig config.LoreTLSConfig

	CostResources []cost.Resource
}

// NewCreatePlan validates inputs and builds a CreatePlan. VPCResolver is called
// only when VPCId/SubnetId are absent from cfg; pass nil to skip resolution
// (dry-run with explicit VPC values, or tests).
func NewCreatePlan(ctx context.Context, cfg config.LoreConfig, account, region string, resolver cloud.VPCResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("lore.amiId is required. Provide an AMI ID that contains the loreserver binary.\nSee: docs/lore-ami.md")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m5.xlarge"
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 500
	}
	allowedCIDR := cfg.AllowedCIDR
	if allowedCIDR == "" {
		allowedCIDR = "10.0.0.0/8"
	}

	storeBackend := normalizeStoreBackend(cfg.StoreBackend)
	storeBucket := ResolveStoreBucket(cfg.StoreBucket, account, region, storeBackend)

	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}

	return &CreatePlan{
		Account:             account,
		Region:              region,
		AmiID:               cfg.AmiID,
		InstanceType:        instanceType,
		VolumeSize:          volumeSize,
		GRPCPort:            DefaultGRPCPort,
		HTTPPort:            DefaultHTTPPort,
		AllowedCIDR:         allowedCIDR,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		SGName:              "fabrica-lore-sg",
		InstanceName:        "fabrica-lore",
		StoreBackend:        storeBackend,
		StoreBucket:         storeBucket,
		StoreTables:         s3StoreTables(storeBackend, storeBucket),
		RoleName:            "fabrica-lore-role",
		InstanceProfileName: "fabrica-lore-profile",
		TLSConfig:           cfg.TLSConfig,
		CostResources:       CostResources(cfg),
	}, nil
}

func normalizeStoreBackend(raw string) string {
	b := strings.ToLower(strings.TrimSpace(raw))
	if b == "" {
		return StoreBackendLocal
	}
	if b != StoreBackendS3 {
		return StoreBackendLocal
	}
	return b
}

// ResolveStoreBucket returns the configured Lore store S3 bucket, or the
// default fabrica-lore-store-<account>-<region> name when unset. Empty for
// any backend that does not use an S3 store.
func ResolveStoreBucket(bucket, account, region, backend string) string {
	if backend != StoreBackendS3 {
		return ""
	}
	if bucket != "" {
		return bucket
	}
	return fmt.Sprintf("fabrica-lore-store-%s-%s", account, region)
}

// s3StoreTables returns the four store table names for the s3 backend, or nil
// for any other backend (local omits DynamoDB entirely).
func s3StoreTables(backend, bucket string) []string {
	if backend != StoreBackendS3 {
		return nil
	}
	return StoreTableNames(bucket)
}
