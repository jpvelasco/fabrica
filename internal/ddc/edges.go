package ddc

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// EdgeOptions overrides an edge region's plan defaults. Empty/zero values fall
// back to the ddc config (or module defaults), matching setup behavior.
type EdgeOptions struct {
	AmiID        string
	InstanceType string
	VolumeSize   int
	VPCID        string
	SubnetID     string
}

// EdgePlan is everything needed to provision one edge node in a peer region.
// No AWS SDK types. The edge shares the home region's blob bucket and IAM
// instance profile; only the SG and EC2 instance are region-scoped.
type EdgePlan struct {
	Account             string
	Region              string
	AmiID               string
	InstanceType        string
	VolumeSize          int
	PublicPort          int
	InternalPort        int
	AllowedCIDR         string
	InternalCIDR        string
	VPCID               string
	SubnetID            string
	DefaultVPC          bool
	Bucket              string
	Namespace           string
	SGName              string
	InstanceName        string
	InstanceProfileName string
	CostResources       []cost.Resource
}

// regionPattern is a light AWS region-name check; AWS rejects invalid regions
// at the API boundary, this just catches typos early.
var regionPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// NewEdgePlan validates config and builds the plan for one additional DDC
// region. The resolver must be bound to the target region (see cloud.RegionProvider).
func NewEdgePlan(ctx context.Context, cfg config.DDCConfig, account, homeRegion, region string, opts EdgeOptions, resolver cloud.VPCResolver) (*EdgePlan, error) {
	if region == "" {
		return nil, fmt.Errorf("ddc edge: region is required. Usage: fabrica ddc region add REGION")
	}
	if !regionPattern.MatchString(region) {
		return nil, fmt.Errorf("ddc edge: %q is not a valid AWS region name (expected e.g. us-west-2 or eu-west-1)", region)
	}
	if region == homeRegion {
		return nil, fmt.Errorf("ddc edge: %q is the home region — add a different region, or use 'fabrica ddc setup' for the home stack", region)
	}

	amiID := opts.AmiID
	if amiID == "" {
		amiID = cfg.AmiID
	}
	if amiID == "" {
		return nil, fmt.Errorf("ddc edge: an AMI ID is required. Pass --ami-id or set ddc.amiId.\n" +
			"The edge AMI must exist in the target region — copy the home AMI there first\n" +
			"(aws ec2 copy-image --source-region <home> --region <target> --name ...).\nSee: docs/ddc-ami.md")
	}

	def := resolveDefaults(cfg, account, homeRegion)
	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, opts.VPCID, opts.SubnetID, resolver)
	if err != nil {
		return nil, err
	}

	instanceType := opts.InstanceType
	if instanceType == "" {
		instanceType = def.instanceType
	}
	volumeSize := opts.VolumeSize
	if volumeSize <= 0 {
		volumeSize = def.volumeSize
	}

	return &EdgePlan{
		Account:             account,
		Region:              region,
		AmiID:               amiID,
		InstanceType:        instanceType,
		VolumeSize:          volumeSize,
		PublicPort:          def.publicPort,
		InternalPort:        def.internalPort,
		AllowedCIDR:         def.allowedCIDR,
		InternalCIDR:        def.internalCIDR,
		VPCID:               vpcID,
		SubnetID:            subnetID,
		DefaultVPC:          defaultVPC,
		Bucket:              def.bucket,
		Namespace:           def.namespace,
		SGName:              fmt.Sprintf("fabrica-ddc-sg-%s", region),
		InstanceName:        fmt.Sprintf("fabrica-ddc-edge-%s", region),
		InstanceProfileName: "fabrica-ddc-profile",
		CostResources:       EdgeCostResources(cfg, opts),
	}, nil
}

// EdgeCostResources returns the monthly cost inputs for one edge node:
// an EC2 instance + gp3 volume, reusing the registered estimators.
func EdgeCostResources(cfg config.DDCConfig, opts EdgeOptions) []cost.Resource {
	instanceType := opts.InstanceType
	if instanceType == "" {
		instanceType = cfg.InstanceType
	}
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := opts.VolumeSize
	if volumeSize <= 0 {
		volumeSize = cfg.VolumeSize
	}
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	return []cost.Resource{
		{TypeName: cloud.TypeAWSEC2Instance, Name: instanceType},
		{TypeName: cloud.TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}

// EdgeResource is an edge node's recorded identifiers, read from module state.
// It is the state-derived view displayed by ddc status (no live claims).
type EdgeResource struct {
	Region       string
	InstanceID   string
	SGID         string
	InstanceType string
	VolumeSize   int
}

// EdgeRegions extracts the additional-region edge nodes recorded in module
// state. The co-located home edge is excluded; results are sorted by region.
// The status layer displays these without probing — replication health is an
// operator-verified, not a fabricated, signal.
func EdgeRegions(resources []state.ModuleResource, homeRegion string) []EdgeResource {
	// region → resource (dedupe by region, last write wins for each field)
	byRegion := map[string]*EdgeResource{}
	for _, r := range resources {
		region := property(r, "region")
		if region == "" || region == homeRegion || property(r, "role") != RoleEdge {
			continue
		}
		er := byRegion[region]
		if er == nil {
			er = &EdgeResource{Region: region}
			byRegion[region] = er
		}
		switch r.TypeName {
		case cloud.TypeAWSEC2Instance:
			er.InstanceID = r.Identifier
			er.InstanceType = property(r, "instanceType")
			er.VolumeSize = atoiOrZero(property(r, "volumeSize"))
		case cloud.TypeAWSEC2SecurityGroup:
			er.SGID = r.Identifier
		}
	}
	out := make([]EdgeResource, 0, len(byRegion))
	for _, er := range byRegion {
		out = append(out, *er)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region < out[j].Region })
	return out
}

// EdgeExists reports whether an extra edge node is recorded for region.
func EdgeExists(resources []state.ModuleResource, region string) bool {
	for _, r := range resources {
		if property(r, "region") == region && property(r, "role") == RoleEdge {
			return true
		}
	}
	return false
}

// EdgeInstanceExists reports whether the instance for region is already
// recorded (used to resume a partial region add).
func EdgeInstanceExists(resources []state.ModuleResource, region string) bool {
	for _, r := range resources {
		if property(r, "region") == region && property(r, "role") == RoleEdge && r.TypeName == cloud.TypeAWSEC2Instance {
			return true
		}
	}
	return false
}

func property(r state.ModuleResource, key string) string {
	if r.Properties == nil {
		return ""
	}
	return r.Properties[key]
}

func atoiOrZero(s string) int {
	n := 0
	fmt.Sscanf(s, "%d", &n)
	return n
}
