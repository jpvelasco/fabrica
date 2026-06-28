package horde

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	TypeAWSEC2Instance = "AWS::EC2::Instance"
	TypeAWSEC2Volume   = "AWS::EC2::Volume"
)

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

	SGName       string
	InstanceName string

	CostResources []cost.Resource
}

func NewCreatePlan(ctx context.Context, cfg config.HordeConfig, account, region string, resolver cloud.VPCResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,\nand the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m7i.2xlarge"
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
	allowedCIDR := cfg.AllowedCIDR
	if allowedCIDR == "" {
		allowedCIDR = "10.0.0.0/8"
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

	return &CreatePlan{
		Account:      account,
		Region:       region,
		AmiID:        cfg.AmiID,
		InstanceType: instanceType,
		VolumeSize:   volumeSize,
		Port:         port,
		GRPCPort:     grpcPort,
		AllowedCIDR:  allowedCIDR,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		DefaultVPC:   defaultVPC,
		SGName:       "fabrica-horde-sg",
		InstanceName: "fabrica-horde",
		CostResources: []cost.Resource{
			{TypeName: TypeAWSEC2Instance, Name: instanceType},
			{TypeName: TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
		},
	}, nil
}
