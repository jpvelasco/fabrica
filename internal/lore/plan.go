package lore

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/topology"
)

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

	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}

	return &CreatePlan{
		Account:       account,
		Region:        region,
		AmiID:         cfg.AmiID,
		InstanceType:  instanceType,
		VolumeSize:    volumeSize,
		GRPCPort:      DefaultGRPCPort,
		HTTPPort:      DefaultHTTPPort,
		AllowedCIDR:   allowedCIDR,
		VPCID:         vpcID,
		SubnetID:      subnetID,
		DefaultVPC:    defaultVPC,
		SGName:        "fabrica-lore-sg",
		InstanceName:  "fabrica-lore",
		CostResources: CostResources(cfg),
	}, nil
}
