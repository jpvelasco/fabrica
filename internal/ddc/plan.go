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
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	backend := normalizeBackend(cfg.Backend)
	def := resolveDefaults(cfg, account, region)
	vpcID, subnetID, defaultVPC, err := resolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}

	top := topology.NewHomeCoLocated(region, topology.NodeSpec{
		InstanceType: def.instanceType,
		AmiID:        cfg.AmiID,
		VolumeSize:   def.volumeSize,
	})
	if err := top.Validate(); err != nil {
		return nil, err
	}

	return &SetupPlan{
		Account:             account,
		Region:              region,
		Backend:             backend,
		AmiID:               cfg.AmiID,
		ScyllaAmiID:         cfg.ScyllaAmiID,
		InstanceType:        def.instanceType,
		VolumeSize:          def.volumeSize,
		ScyllaInstanceType:  scyllaInstanceType(cfg.ScyllaInstanceType),
		ScyllaVolumeSize:    scyllaVolumeSize(cfg.ScyllaVolumeSize),
		PublicPort:          def.publicPort,
		InternalPort:        def.internalPort,
		AllowedCIDR:         def.allowedCIDR,
		InternalCIDR:        def.internalCIDR,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		Bucket:              def.bucket,
		Namespace:           def.namespace,
		SGName:              "fabrica-ddc-sg",
		InstanceName:        "fabrica-ddc",
		ScyllaInstanceName:  "fabrica-ddc-scylla",
		RoleName:            "fabrica-ddc-role",
		InstanceProfileName: "fabrica-ddc-profile",
		Topology:            top,
		CostResources:       CostResources(cfg),
	}, nil
}

type ddcDefaults struct {
	instanceType string
	volumeSize   int
	publicPort   int
	internalPort int
	allowedCIDR  string
	internalCIDR string
	bucket       string
	namespace    string
}

func validateConfig(cfg config.DDCConfig) error {
	if cfg.AmiID == "" {
		return fmt.Errorf("ddc.amiId is required. Provide an AMI ID that contains Unreal Cloud DDC (Jupiter).\nSee: docs/ddc-ami.md")
	}

	backend := normalizeBackend(cfg.Backend)
	if backend != BackendZen && backend != BackendScylla {
		return fmt.Errorf("ddc.backend must be %q or %q, got %q", BackendZen, BackendScylla, cfg.Backend)
	}
	if backend == BackendScylla && cfg.ScyllaAmiID == "" {
		return fmt.Errorf("ddc.scyllaAmiId is required when backend is scylla.\n" +
			"Scylla backend in V1 is a single-node bootstrap path only — not production HA.\n" +
			"Prefer backend: zen unless you explicitly need Scylla and accept the limitations.\nSee: docs/ddc-ami.md")
	}
	return nil
}

func normalizeBackend(raw string) string {
	b := strings.ToLower(strings.TrimSpace(raw))
	if b == "" {
		return BackendZen
	}
	return b
}

func resolveDefaults(cfg config.DDCConfig, account, region string) ddcDefaults {
	return ddcDefaults{
		instanceType: instanceTypeOrDefault(cfg.InstanceType),
		volumeSize:   volumeSizeOrDefault(cfg.VolumeSize),
		publicPort:   publicPortOrDefault(cfg.PublicPort),
		internalPort: internalPortOrDefault(cfg.InternalPort),
		allowedCIDR:  cidrOrDefault(cfg.AllowedCIDR),
		internalCIDR: internalCIDROrDefault(cfg.InternalCIDR),
		bucket:       bucketOrDefault(cfg.Bucket, account, region),
		namespace:    namespaceOrDefault(cfg.Namespace),
	}
}

func resolveVPC(ctx context.Context, vpcID, subnetID string, resolver cloud.VPCResolver) (string, string, bool, error) {
	return topology.ResolveVPC(ctx, vpcID, subnetID, resolver)
}

func instanceTypeOrDefault(v string) string {
	if v == "" {
		return DefaultInstanceType
	}
	return v
}

func volumeSizeOrDefault(v int) int {
	if v <= 0 {
		return DefaultVolumeSize
	}
	return v
}

func scyllaInstanceType(v string) string {
	if v == "" {
		return DefaultScyllaInstanceType
	}
	return v
}

func scyllaVolumeSize(v int) int {
	if v <= 0 {
		return DefaultScyllaVolumeSize
	}
	return v
}

func publicPortOrDefault(v int) int {
	if v <= 0 {
		return DefaultPublicPort
	}
	return v
}

func internalPortOrDefault(v int) int {
	if v <= 0 {
		return DefaultInternalPort
	}
	return v
}

func cidrOrDefault(v string) string {
	if v == "" {
		return DefaultAllowedCIDR
	}
	return v
}

func internalCIDROrDefault(v string) string {
	if v == "" {
		return DefaultAllowedCIDR
	}
	return v
}

func namespaceOrDefault(v string) string {
	if v == "" {
		return DefaultNamespace
	}
	return v
}

func bucketOrDefault(v, account, region string) string {
	if v == "" {
		return fmt.Sprintf("fabrica-ddc-%s-%s", account, region)
	}
	return v
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
