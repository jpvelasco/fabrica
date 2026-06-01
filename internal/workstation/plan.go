package workstation

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	typeEC2Instance = "AWS::EC2::Instance"
	typeEC2Volume   = "AWS::EC2::Volume"
)

// CreatePlan holds everything needed to provision a workstation. No AWS SDK
// types — callers execute the plan via rt.Provider.Resources().
type CreatePlan struct {
	Account            string
	Region             string
	AmiID              string
	InstanceType       string
	VolumeSize         int
	DCVPort            int
	IdleTimeoutMinutes int
	AllowedCIDR        string
	MountPerforce      bool
	VPCID              string
	SubnetID           string
	DefaultVPC         bool

	SGName       string
	InstanceName string

	CostResources []cost.Resource
}

// NewCreatePlan validates inputs and builds a CreatePlan. VPCResolver is called
// only when VPCId/SubnetId are absent from cfg; pass nil to skip resolution.
func NewCreatePlan(ctx context.Context, cfg config.WorkstationConfig, account, region string, resolver VPCResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("workstation.amiId is required. Provide a NICE DCV-enabled AMI ID.\nSee: https://docs.aws.amazon.com/dcv/latest/adminguide/setting-up-installing.html")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	dcvPort := DefaultDCVPort
	idleTimeout := cfg.IdleTimeoutMinutes
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeoutMinutes
	}
	allowedCIDR := cfg.AllowedCIDR
	if allowedCIDR == "" {
		allowedCIDR = DefaultAllowedCIDR
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
		Account:            account,
		Region:             region,
		AmiID:              cfg.AmiID,
		InstanceType:       instanceType,
		VolumeSize:         volumeSize,
		DCVPort:            dcvPort,
		IdleTimeoutMinutes: idleTimeout,
		AllowedCIDR:        allowedCIDR,
		VPCID:              vpcID,
		SubnetID:           subnetID,
		DefaultVPC:         defaultVPC,
		SGName:             "fabrica-workstation-sg",
		InstanceName:       "fabrica-workstation",
		CostResources: []cost.Resource{
			{TypeName: typeEC2Instance, Name: instanceType},
			{TypeName: typeEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
		},
	}, nil
}
