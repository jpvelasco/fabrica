package horde

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// DefaultInstanceType is the EC2 shape used when config omits instanceType.
const DefaultInstanceType = "m7i.2xlarge"

// DefaultAllowedCIDR is the security-group fallback when no explicit CIDR is
// configured and the VPC CIDR cannot be resolved.
const DefaultAllowedCIDR = "10.0.0.0/8"

type CreatePlan struct {
	Account      string
	Region       string
	AmiID        string
	InstanceType string
	VolumeSize   int
	Port         int
	GRPCPort     int
	AllowedCIDR  string
	VPCID        string
	SubnetID     string
	DefaultVPC   bool

	SGName              string
	InstanceName        string
	RoleName            string
	InstanceProfileName string

	CostResources []cost.Resource
}

func NewCreatePlan(ctx context.Context, cfg config.HordeConfig, account, region string, resolver cloud.VPCResolver, cidrResolver cloud.VPCCIDRResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,\nand the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 100
	}
	port := cfg.Port
	if port <= 0 {
		port = 5000
	}
	grpcPort := cfg.GRPCPort
	if grpcPort <= 0 {
		grpcPort = 5002
	}

	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}
	allowedCIDR := topology.ResolveAllowedCIDR(ctx, cfg.AllowedCIDR, vpcID, cidrResolver, DefaultAllowedCIDR)

	return &CreatePlan{
		Account:             account,
		Region:              region,
		AmiID:               cfg.AmiID,
		InstanceType:        instanceType,
		VolumeSize:          volumeSize,
		Port:                port,
		GRPCPort:            grpcPort,
		AllowedCIDR:         allowedCIDR,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		SGName:              "fabrica-horde-sg",
		InstanceName:        "fabrica-horde",
		RoleName:            "fabrica-horde-role",
		InstanceProfileName: "fabrica-horde-profile",
		CostResources:       CostResources(cfg),
	}, nil
}
