package workstation

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	typeEC2Instance = "AWS::EC2::Instance"
	typeEC2Volume   = "AWS::EC2::Volume"

	// TemplateArtist targets GPU-heavy workloads (NVIDIA L4).
	TemplateArtist = "artist"
	// TemplateProgrammer targets CPU-bound development workloads.
	TemplateProgrammer = "programmer"

	// Artist template defaults (g6.xlarge — NVIDIA L4 GPU).
	ArtistInstanceType = "g6.xlarge"
	ArtistVolumeSize   = 200

	// Programmer template defaults (c7i.xlarge — Intel Sapphire Rapids).
	ProgrammerInstanceType = "c7i.xlarge"
	ProgrammerVolumeSize   = 100
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
	VPCID              string
	SubnetID           string
	DefaultVPC         bool
	MountPerforce      bool
	PerforceServerAddr string

	SGName       string
	InstanceName string

	CostResources []cost.Resource
}

// NewCreatePlan validates inputs and builds a CreatePlan. VPCResolver is called
// only when VPCId/SubnetId are absent from cfg; pass nil to skip resolution.
// template overrides instanceType and volumeSize when non-empty.
// perforceAddr, when non-empty, is the Perforce server address to mount; it is
// threaded through to UserDataConfig at apply time and toggles MountPerforce.
func NewCreatePlan(ctx context.Context, cfg config.WorkstationConfig, account, region string, resolver cloud.VPCResolver, tmpl, perforceAddr string) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("workstation.amiId is required. Provide a NICE DCV-enabled AMI ID.\nSee: https://docs.aws.amazon.com/dcv/latest/adminguide/setting-up-installing.html")
	}

	instanceType := cfg.InstanceType
	volumeSize := cfg.VolumeSize

	switch tmpl {
	case TemplateArtist:
		instanceType = ArtistInstanceType
		volumeSize = ArtistVolumeSize
	case TemplateProgrammer:
		instanceType = ProgrammerInstanceType
		volumeSize = ProgrammerVolumeSize
	case "":
		// no template — fall through to individual defaults below
	default:
		return nil, fmt.Errorf("unknown template %q: must be %q or %q", tmpl, TemplateArtist, TemplateProgrammer)
	}

	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
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
		MountPerforce:      perforceAddr != "",
		PerforceServerAddr: perforceAddr,
		SGName:             "fabrica-workstation-sg",
		InstanceName:       "fabrica-workstation",
		CostResources: []cost.Resource{
			{TypeName: typeEC2Instance, Name: instanceType},
			{TypeName: typeEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
		},
	}, nil
}
