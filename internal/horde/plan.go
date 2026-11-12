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

// DefaultPort and DefaultGRPCPort are the coordinator ports used when the
// config omits them (HTTP/web UI and gRPC for agent connections).
const (
	DefaultPort     = 5000
	DefaultGRPCPort = 5002
)

// DefaultAllowedCIDR is the security-group fallback when no explicit CIDR is
// configured and the VPC CIDR cannot be resolved.
const DefaultAllowedCIDR = "10.0.0.0/8"

// DefaultSGName is the coordinator security group name.
const DefaultSGName = "fabrica-horde-sg"

type CreatePlan struct {
	Connection   Connection
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

	// RootDeviceName is the AMI's root device name (for example /dev/sda1),
	// read from the AMI at create time. The encrypted root volume mapping is
	// built on it; empty refuses launch (fail-closed, #513).
	RootDeviceName string

	// IAM is the resolved path and permissions boundary for the instance role.
	IAM config.IAM

	// ServiceTokenParameter is the per-create SSM SecureString cloud-init
	// publishes the Horde service-account token to. The create layer mints it
	// (ServiceTokenParameterFor) before the plan is printed, so the role scope,
	// cloud-init, and the state record name the same parameter.
	ServiceTokenParameter string

	CostResources []cost.Resource
}

// RequiredResources lists the resource TypeNames a complete Horde module
// records in state. Create retries compare a module's recorded inventory
// against this list to separate a fully provisioned module (in any
// transitional status) from a partial one.
func (p CreatePlan) RequiredResources() []string {
	return []string{
		cloud.TypeAWSEC2SecurityGroup,
		cloud.TypeAWSIAMRole,
		cloud.TypeAWSIAMInstanceProfile,
		cloud.TypeAWSEC2Instance,
	}
}

func NewCreatePlan(ctx context.Context, cfg config.HordeConfig, account, region string, resolver cloud.VPCResolver, cidrResolver cloud.VPCCIDRResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,\nand the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md")
	}
	if err := cfg.IAM.Validate(); err != nil {
		return nil, err
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 100
	}
	connection, err := ConnectionFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	if connection.TLS {
		return nil, fmt.Errorf("TLS provisioning is not enabled in this build; use the completed Horde TLS release")
	}

	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}
	allowedCIDR := topology.ResolveAllowedCIDR(ctx, cfg.AllowedCIDR, vpcID, cidrResolver, DefaultAllowedCIDR)

	return &CreatePlan{
		Connection:          connection,
		Account:             account,
		Region:              region,
		AmiID:               cfg.AmiID,
		InstanceType:        instanceType,
		VolumeSize:          volumeSize,
		Port:                connection.Port,
		GRPCPort:            connection.GRPCPort,
		AllowedCIDR:         allowedCIDR,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		SGName:              DefaultSGName,
		InstanceName:        "fabrica-horde",
		RoleName:            "fabrica-horde-role",
		InstanceProfileName: "fabrica-horde-profile",
		IAM:                 cfg.IAM,
		CostResources:       CostResources(cfg),
	}, nil
}
