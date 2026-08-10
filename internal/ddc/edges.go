package ddc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/topology"
)

const edgeProbeTimeout = 3 * time.Second

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

// atoiOrZero parses an integer property, returning 0 on missing or invalid.
func atoiOrZero(s string) int {
	n := 0
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// EdgeStatus is the live status result for one edge node, produced by
// ProbeEdges. It carries instance state from Cloud Control and an optional
// health probe result.
type EdgeStatus struct {
	// Region is the AWS region where this edge runs.
	Region string
	// InstanceID is the EC2 instance identifier from state.
	InstanceID string
	// InstanceState is the EC2 lifecycle state from Cloud Control
	// (running, stopped, terminated, missing, or empty on error).
	InstanceState string
	// InstanceType is the instance type from Cloud Control (may be empty).
	InstanceType string
	// PrivateIP is the instance private IP from Cloud Control (may be empty).
	PrivateIP string
	// ProbeStatus describes the health probe outcome:
	//   "ready"       — HTTP 200 from /health/ready
	//   "unreachable" — instance running but probe failed/timed out
	//   "skipped"     — probe not attempted (no private IP, not running, etc.)
	ProbeStatus string
	// ProbeError is a short error summary when the probe failed (empty otherwise).
	ProbeError string
}

// ProbeEdgesOptions configures edge probing behavior.
type ProbeEdgesOptions struct {
	// PublicPort is the HTTP port for the /health/ready probe.
	PublicPort int
	// HTTPClient is the client used for health probes. Nil uses a default
	// client with edgeProbeTimeout.
	HTTPClient *http.Client
}

// ProbeEdges probes each edge recorded in state using region-scoped Cloud
// Control Get and an optional HTTP health probe. It returns one EdgeStatus
// per edge, preserving the sorted order of EdgeRegions. Failures per edge
// are soft — one edge's failure does not block others.
func ProbeEdges(ctx context.Context, edges []EdgeResource, provider cloud.Provider, opts ProbeEdgesOptions) []EdgeStatus {
	if len(edges) == 0 {
		return nil
	}

	regionProvider, canProbe := provider.(cloud.RegionProvider)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: edgeProbeTimeout}
	}

	statuses := make([]EdgeStatus, 0, len(edges))
	for _, e := range edges {
		s := EdgeStatus{Region: e.Region, InstanceID: e.InstanceID}

		if !canProbe {
			s.ProbeStatus = "skipped"
			statuses = append(statuses, s)
			continue
		}

		rv, err := regionProvider.WithRegion(ctx, e.Region)
		if err != nil {
			s.ProbeStatus = "skipped"
			s.ProbeError = fmt.Sprintf("cannot reach region %s: %v", e.Region, err)
			statuses = append(statuses, s)
			continue
		}

		if rv.Resources == nil {
			s.ProbeStatus = "skipped"
			s.ProbeError = "region provider has no resource client"
			statuses = append(statuses, s)
			continue
		}

		// Describe the instance via Cloud Control Get.
		res := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: e.InstanceID}
		if err := rv.Resources.Get(ctx, res); err != nil {
			s.InstanceState = "missing"
			s.ProbeStatus = "skipped"
			s.ProbeError = fmt.Sprintf("Cloud Control Get failed: %v", err)
			statuses = append(statuses, s)
			continue
		}

		// Parse ActualState for instance details.
		parseEdgeActualState(res, &s)

		// Probe health if running and private IP is available.
		if s.InstanceState == "running" && s.PrivateIP != "" {
			s.ProbeStatus = probeEdgeHealth(client, s.PrivateIP, opts.PublicPort)
		} else {
			s.ProbeStatus = "skipped"
		}

		statuses = append(statuses, s)
	}
	return statuses
}

// parseEdgeActualState extracts instance fields from Cloud Control ActualState.
func parseEdgeActualState(r *cloud.Resource, s *EdgeStatus) {
	if len(r.ActualState) == 0 {
		return
	}
	var actual struct {
		InstanceType     string `json:"InstanceType"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
		State            struct {
			Name string `json:"Name"`
		} `json:"State"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil {
		return
	}
	s.InstanceType = actual.InstanceType
	s.PrivateIP = actual.PrivateIPAddress
	s.InstanceState = actual.State.Name
}

// probeEdgeHealth performs an HTTP GET /health/ready against the edge instance.
// Returns "ready" on HTTP 200, "unreachable" otherwise.
func probeEdgeHealth(client *http.Client, privateIP string, port int) string {
	url := fmt.Sprintf("http://%s:%d/health/ready", privateIP, port)
	resp, err := client.Get(url)
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "ready"
	}
	return "unreachable"
}
