package ddc

import (
	"context"
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// Backend kinds for ddc.backend / --backend.
const (
	BackendZen    = "zen"
	BackendScylla = "scylla"
)

// Cloud Control / state type names.
const (
	TypeAWSEC2Instance        = "AWS::EC2::Instance"
	TypeAWSEC2SecurityGroup   = "AWS::EC2::SecurityGroup"
	TypeAWSEC2Volume          = "AWS::EC2::Volume"
	TypeAWSS3Bucket           = "AWS::S3::Bucket"
	TypeAWSIAMRole            = "AWS::IAM::Role"
	TypeAWSIAMInstanceProfile = "AWS::IAM::InstanceProfile"
)

// Defaults.
const (
	DefaultInstanceType       = "m7i.xlarge"
	DefaultVolumeSize         = 500
	DefaultScyllaInstanceType = "i4i.large"
	DefaultScyllaVolumeSize   = 500
	DefaultAllowedCIDR        = "10.0.0.0/8"
	DefaultPublicPort         = 80
	DefaultInternalPort       = 8080
	DefaultNamespace          = "deriveddatacache"
	DefaultStorePath          = "/opt/unreal-cloud-ddc/store"
)

// Resource role property values (state Properties.role).
const (
	RoleCoordinator = "coordinator"
	RoleScylla      = "scylla"
	RoleBlob        = "blob"
)

// SetupPlan is everything needed to provision single-region DDC. No AWS SDK types.
type SetupPlan struct {
	Account            string
	Region             string
	Backend            string
	AmiID              string
	ScyllaAmiID        string
	InstanceType       string
	VolumeSize         int
	ScyllaInstanceType string
	ScyllaVolumeSize   int
	PublicPort         int
	InternalPort       int
	AllowedCIDR        string
	InternalCIDR       string
	VPCID              string
	SubnetID           string
	DefaultVPC         bool
	Bucket             string
	Namespace          string

	SGName              string
	InstanceName        string
	ScyllaInstanceName  string
	RoleName            string
	InstanceProfileName string

	Topology topology.Topology

	CostResources []cost.Resource
}

// NewSetupPlan validates config and builds a single home-region SetupPlan.
func NewSetupPlan(ctx context.Context, cfg config.DDCConfig, account, region string, resolver cloud.VPCResolver) (*SetupPlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("ddc.amiId is required. Provide an AMI ID that contains Unreal Cloud DDC (Jupiter).\nSee: docs/ddc-ami.md")
	}

	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = BackendZen
	}
	if backend != BackendZen && backend != BackendScylla {
		return nil, fmt.Errorf("ddc.backend must be %q or %q, got %q", BackendZen, BackendScylla, cfg.Backend)
	}
	if backend == BackendScylla && cfg.ScyllaAmiID == "" {
		return nil, fmt.Errorf("ddc.scyllaAmiId is required when backend is scylla.\n" +
			"Scylla backend in V1 is a single-node bootstrap path only — not production HA.\n" +
			"Prefer backend: zen unless you explicitly need Scylla and accept the limitations.\nSee: docs/ddc-ami.md")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	scyllaType := cfg.ScyllaInstanceType
	if scyllaType == "" {
		scyllaType = DefaultScyllaInstanceType
	}
	scyllaVol := cfg.ScyllaVolumeSize
	if scyllaVol <= 0 {
		scyllaVol = DefaultScyllaVolumeSize
	}
	allowed := cfg.AllowedCIDR
	if allowed == "" {
		allowed = DefaultAllowedCIDR
	}
	internal := cfg.InternalCIDR
	if internal == "" {
		internal = DefaultAllowedCIDR
	}
	publicPort := cfg.PublicPort
	if publicPort <= 0 {
		publicPort = DefaultPublicPort
	}
	internalPort := cfg.InternalPort
	if internalPort <= 0 {
		internalPort = DefaultInternalPort
	}
	ns := cfg.Namespace
	if ns == "" {
		ns = DefaultNamespace
	}
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = fmt.Sprintf("fabrica-ddc-%s-%s", account, region)
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

	top := topology.NewHomeCoLocated(region, topology.NodeSpec{
		InstanceType: instanceType,
		AmiID:        cfg.AmiID,
		VolumeSize:   volumeSize,
	})
	if err := top.Validate(); err != nil {
		return nil, err
	}

	plan := &SetupPlan{
		Account:             account,
		Region:              region,
		Backend:             backend,
		AmiID:               cfg.AmiID,
		ScyllaAmiID:         cfg.ScyllaAmiID,
		InstanceType:        instanceType,
		VolumeSize:          volumeSize,
		ScyllaInstanceType:  scyllaType,
		ScyllaVolumeSize:    scyllaVol,
		PublicPort:          publicPort,
		InternalPort:        internalPort,
		AllowedCIDR:         allowed,
		InternalCIDR:        internal,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		Bucket:              bucket,
		Namespace:           ns,
		SGName:              "fabrica-ddc-sg",
		InstanceName:        "fabrica-ddc",
		ScyllaInstanceName:  "fabrica-ddc-scylla",
		RoleName:            "fabrica-ddc-role",
		InstanceProfileName: "fabrica-ddc-profile",
		Topology:            top,
		CostResources:       CostResources(cfg),
	}
	return plan, nil
}

// WarnOpenCIDR returns a Horde-style warning when cidr is open to the world.
func WarnOpenCIDR(cidr string) string {
	if cidr != "0.0.0.0/0" {
		return ""
	}
	return "WARNING: ddc.allowedCidr is 0.0.0.0/0 — the DDC public API is open\n" +
		"         to the internet. V1 has no OIDC/JWT auth. Restrict this in\n" +
		"         fabrica.yaml (e.g. 10.0.0.0/8 or a VPN CIDR) before production use."
}

// WarnScyllaBootstrap is printed whenever backend=scylla.
func WarnScyllaBootstrap() string {
	return "WARNING: Scylla backend in V1 is a single-node bootstrap path only.\n" +
		"         It is not production HA (no RF=3, no multi-DC).\n" +
		"         Use default zen unless you explicitly need Scylla and accept the limitations."
}
