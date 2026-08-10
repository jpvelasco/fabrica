package ddc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

// fakeRegionProvider is a test double for cloud.RegionProvider that returns
// configurable RegionViews per region.
type fakeRegionProvider struct {
	// regionViews maps region name to the RegionView returned by WithRegion.
	regionViews map[string]cloud.RegionView
	// withRegionErr is returned by WithRegion when non-nil.
	withRegionErr error
}

func (f *fakeRegionProvider) Name() string { return "fake" }

func (f *fakeRegionProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-east-1", nil
}

func (f *fakeRegionProvider) Resources() cloud.ResourceClient { return nil }

func (f *fakeRegionProvider) WithRegion(_ context.Context, region string) (cloud.RegionView, error) {
	if f.withRegionErr != nil {
		return cloud.RegionView{}, f.withRegionErr
	}
	if f.regionViews == nil {
		return cloud.RegionView{}, nil
	}
	return f.regionViews[region], nil
}

// fakeResourceClient is a simple ResourceClient that returns a pre-configured
// resource for Get calls.
type fakeResourceClient struct {
	getResult *cloud.Resource
	getErr    error
}

func (f *fakeResourceClient) Create(_ context.Context, _ *cloud.Resource) error { return nil }

func (f *fakeResourceClient) Get(_ context.Context, res *cloud.Resource) error {
	if f.getErr != nil {
		return f.getErr
	}
	if f.getResult != nil && res != nil {
		res.ActualState = f.getResult.ActualState
	}
	return nil
}

func (f *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }

func (f *fakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }

func (f *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

// makeInstanceActualState builds a Cloud Control ActualState JSON for an EC2
// instance with the given state name, private IP, and instance type.
func makeInstanceActualState(stateName, privateIP, instanceType string) json.RawMessage {
	m := map[string]any{
		"InstanceType":     instanceType,
		"PrivateIpAddress": privateIP,
		"State":            map[string]string{"Name": stateName},
	}
	b, _ := json.Marshal(m)
	return b
}

func TestNewEdgePlanDefaults(t *testing.T) {
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID:    "ami-ddc",
		VPCId:    "vpc-home",
		SubnetId: "subnet-home",
	}, "123456789012", "us-east-1", "eu-west-1", EdgeOptions{VPCID: "vpc-edge", SubnetID: "subnet-edge"}, nil)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.Region != "eu-west-1" {
		t.Fatalf("Region = %q", plan.Region)
	}
	if plan.AmiID != "ami-ddc" {
		t.Fatalf("AmiID = %q", plan.AmiID)
	}
	if plan.InstanceType != DefaultInstanceType || plan.VolumeSize != DefaultVolumeSize {
		t.Fatalf("defaults: %s %d", plan.InstanceType, plan.VolumeSize)
	}
	// Edge shares the home blob bucket, not a region-specific one.
	if plan.Bucket != "fabrica-ddc-123456789012-us-east-1" {
		t.Fatalf("Bucket = %q", plan.Bucket)
	}
	if plan.SGName != "fabrica-ddc-sg-eu-west-1" || plan.InstanceName != "fabrica-ddc-edge-eu-west-1" {
		t.Fatalf("names: %s %s", plan.SGName, plan.InstanceName)
	}
	if plan.InstanceProfileName != "fabrica-ddc-profile" {
		t.Fatalf("profile = %q", plan.InstanceProfileName)
	}
	if plan.PublicPort != DefaultPublicPort || plan.AllowedCIDR != DefaultAllowedCIDR {
		t.Fatalf("ports/cidr: %d %s", plan.PublicPort, plan.AllowedCIDR)
	}
	if plan.VPCID != "vpc-edge" || plan.SubnetID != "subnet-edge" {
		t.Fatalf("vpc: %s %s", plan.VPCID, plan.SubnetID)
	}
	if plan.DefaultVPC {
		t.Fatal("DefaultVPC should be false when flags are set")
	}
	if len(plan.CostResources) != 2 {
		t.Fatalf("cost resources = %d", len(plan.CostResources))
	}
}

func TestNewEdgePlanOverrides(t *testing.T) {
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID:        "ami-home",
		InstanceType: "m7i.xlarge",
		VolumeSize:   500,
		VPCId:        "vpc-home",
		SubnetId:     "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{
		AmiID:        "ami-edge",
		InstanceType: "m7i.large",
		VolumeSize:   250,
		VPCID:        "vpc-eu",
		SubnetID:     "subnet-eu",
	}, nil)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.AmiID != "ami-edge" || plan.InstanceType != "m7i.large" || plan.VolumeSize != 250 {
		t.Fatalf("overrides: %s %s %d", plan.AmiID, plan.InstanceType, plan.VolumeSize)
	}
	if plan.VPCID != "vpc-eu" {
		t.Fatalf("VPCID = %q", plan.VPCID)
	}
}

func TestNewEdgePlanResolvesVPCFromResolver(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-eu", SubnetID: "subnet-eu"}
	plan, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID: "ami-ddc", VPCId: "vpc-home", SubnetId: "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{}, resolver)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.VPCID != "vpc-eu" || plan.SubnetID != "subnet-eu" {
		t.Fatalf("vpc: %s %s", plan.VPCID, plan.SubnetID)
	}
	if !plan.DefaultVPC {
		t.Fatal("DefaultVPC should be true when resolved")
	}
	if resolver.Calls != 1 {
		t.Fatalf("resolver calls = %d", resolver.Calls)
	}
}

func TestNewEdgePlanSkipsResolverWhenFlagsSet(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-other"}
	_, err := NewEdgePlan(context.Background(), config.DDCConfig{
		AmiID: "ami-ddc", VPCId: "vpc-home", SubnetId: "subnet-home",
	}, "1", "us-east-1", "eu-west-1", EdgeOptions{VPCID: "vpc-set", SubnetID: "subnet-set"}, resolver)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if resolver.Calls != 0 {
		t.Fatalf("resolver calls = %d, want 0", resolver.Calls)
	}
}

func TestNewEdgePlanValidation(t *testing.T) {
	cfg := config.DDCConfig{AmiID: "ami-ddc", VPCId: "v", SubnetId: "s"}
	cases := []struct {
		name       string
		home       string
		region     string
		wantSubstr string
	}{
		{name: "empty region", home: "us-east-1", region: "", wantSubstr: "region is required"},
		{name: "bad region format", home: "us-east-1", region: "US_EAST_1", wantSubstr: "not a valid AWS region name"},
		{name: "home region", home: "us-east-1", region: "us-east-1", wantSubstr: "is the home region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewEdgePlan(context.Background(), cfg, "1", tc.home, tc.region, EdgeOptions{}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err = %v, want substr %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestNewEdgePlanRequiresAmi(t *testing.T) {
	_, err := NewEdgePlan(context.Background(), config.DDCConfig{}, "1", "us-east-1", "eu-west-1", EdgeOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "--ami-id") {
		t.Fatalf("err = %v", err)
	}
}

func TestEdgeCostResources(t *testing.T) {
	out := EdgeCostResources(config.DDCConfig{}, EdgeOptions{})
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Name != DefaultInstanceType {
		t.Fatalf("instance = %q", out[0].Name)
	}
	if out[1].Name != "gp3-500GiB" {
		t.Fatalf("volume = %q", out[1].Name)
	}
	withOpts := EdgeCostResources(config.DDCConfig{InstanceType: "m7i.large", VolumeSize: 250}, EdgeOptions{InstanceType: "c7i.large", VolumeSize: 100})
	if withOpts[0].Name != "c7i.large" || withOpts[1].Name != "gp3-100GiB" {
		t.Fatalf("opts override: %+v", withOpts)
	}
	withCfg := EdgeCostResources(config.DDCConfig{InstanceType: "m7i.large", VolumeSize: 250}, EdgeOptions{})
	if withCfg[0].Name != "m7i.large" || withCfg[1].Name != "gp3-250GiB" {
		t.Fatalf("config fallback: %+v", withCfg)
	}
}

func TestEdgeRegions(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home", Properties: map[string]string{"region": "us-east-1"}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home", Properties: map[string]string{"region": "us-east-1", "role": RoleCoordinator}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{
			"region": "eu-west-1", "role": RoleEdge, "instanceType": "m7i.large", "volumeSize": "250",
		}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": RoleEdge}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": RoleEdge}},
	}
	edges := EdgeRegions(resources, "us-east-1")
	if len(edges) != 2 {
		t.Fatalf("len = %d, want 2", len(edges))
	}
	if edges[0].Region != "ap-southeast-2" || edges[1].Region != "eu-west-1" {
		t.Fatalf("order = %s, %s", edges[0].Region, edges[1].Region)
	}
	if edges[1].InstanceID != "i-eu" || edges[1].SGID != "sg-eu" {
		t.Fatalf("eu edge = %+v", edges[1])
	}
	if edges[1].InstanceType != "m7i.large" || edges[1].VolumeSize != 250 {
		t.Fatalf("eu shape = %+v", edges[1])
	}
}

func TestEdgeRegionsNoEdges(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home", Properties: map[string]string{"region": "us-east-1", "role": RoleCoordinator}},
	}
	if got := EdgeRegions(resources, "us-east-1"); len(got) != 0 {
		t.Fatalf("got %d edges, want 0", len(got))
	}
}

func TestEdgeRegionsSkipsHomeEdge(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home-edge", Properties: map[string]string{"region": "us-east-1", "role": RoleEdge}},
	}
	if got := EdgeRegions(resources, "us-east-1"); len(got) != 0 {
		t.Fatalf("home co-located edge must be excluded, got %+v", got)
	}
}

func TestEdgeExistsAndInstance(t *testing.T) {
	resources := []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge}},
	}
	if EdgeExists(resources, "eu-west-1") != true {
		t.Fatal("EdgeExists should be true (SG counts)")
	}
	if EdgeExists(resources, "us-west-2") {
		t.Fatal("EdgeExists should be false for other region")
	}
	if EdgeInstanceExists(resources, "eu-west-1") {
		t.Fatal("EdgeInstanceExists should be false (no instance yet)")
	}
	resources = append(resources, state.ModuleResource{
		TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{"region": "eu-west-1", "role": RoleEdge},
	})
	if !EdgeInstanceExists(resources, "eu-west-1") {
		t.Fatal("EdgeInstanceExists should be true")
	}
}

func TestEdgePlanPropertyHelpers(t *testing.T) {
	if property(state.ModuleResource{}, "nope") != "" {
		t.Fatal("expected empty property")
	}
	if atoiOrZero("") != 0 || atoiOrZero("abc") != 0 || atoiOrZero("42") != 42 {
		t.Fatal("atoiOrZero mishandled input")
	}
}

func TestProbeEdgesEmpty(t *testing.T) {
	result := ProbeEdges(context.Background(), nil, &fakeRegionProvider{}, ProbeEdgesOptions{})
	if result != nil {
		t.Fatalf("expected nil for empty edges, got %d", len(result))
	}
	result = ProbeEdges(context.Background(), []EdgeResource{}, &fakeRegionProvider{}, ProbeEdgesOptions{})
	if result != nil {
		t.Fatalf("expected nil for empty edges, got %d", len(result))
	}
}

func TestProbeEdgesNoRegionProvider(t *testing.T) {
	// Use a provider that does NOT implement RegionProvider.
	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	// cloud.TestVPCResolver does not implement RegionProvider.
	provider := &noRegionProvider{}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{})
	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	if result[0].ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped", result[0].ProbeStatus)
	}
}

// noRegionProvider is a provider that deliberately does NOT implement RegionProvider.
type noRegionProvider struct{}

func (n *noRegionProvider) Name() string { return "fake" }
func (n *noRegionProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-east-1", nil
}
func (n *noRegionProvider) Resources() cloud.ResourceClient { return nil }

func TestProbeEdgesRunningAndUnreachable(t *testing.T) {
	// Running instance with private IP should attempt probe; since 127.0.0.1:80
	// won't respond with 200, the probe result is "unreachable".
	actualState := makeInstanceActualState("running", "127.0.0.1", "m7i.large")
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{
					getResult: &cloud.Resource{ActualState: actualState},
				},
			},
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.Region != "eu-west-1" {
		t.Fatalf("Region = %q", s.Region)
	}
	if s.InstanceID != "i-eu" {
		t.Fatalf("InstanceID = %q", s.InstanceID)
	}
	if s.InstanceState != "running" {
		t.Fatalf("InstanceState = %q, want running", s.InstanceState)
	}
	if s.InstanceType != "m7i.large" {
		t.Fatalf("InstanceType = %q, want m7i.large", s.InstanceType)
	}
	if s.PrivateIP != "127.0.0.1" {
		t.Fatalf("PrivateIP = %q, want 127.0.0.1", s.PrivateIP)
	}
	if s.ProbeStatus != "unreachable" {
		t.Fatalf("ProbeStatus = %q, want unreachable", s.ProbeStatus)
	}
}

func TestProbeEdgesRunningAndReadyWithServer(t *testing.T) {
	// Set up a test server that returns 200 for /health/ready.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Parse the server address so we can inject host and port separately.
	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	actualState := makeInstanceActualState("running", host, "m7i.large")
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{
					getResult: &cloud.Resource{ActualState: actualState},
				},
			},
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: port})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.InstanceState != "running" {
		t.Fatalf("InstanceState = %q, want running", s.InstanceState)
	}
	if s.ProbeStatus != "ready" {
		t.Fatalf("ProbeStatus = %q, want ready", s.ProbeStatus)
	}
}

func TestProbeEdgesStopped(t *testing.T) {
	actualState := makeInstanceActualState("stopped", "10.0.1.5", "m7i.large")
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{
					getResult: &cloud.Resource{ActualState: actualState},
				},
			},
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.InstanceState != "stopped" {
		t.Fatalf("InstanceState = %q, want stopped", s.InstanceState)
	}
	if s.ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped for stopped instance", s.ProbeStatus)
	}
}

func TestProbeEdgesTerminated(t *testing.T) {
	actualState := makeInstanceActualState("terminated", "", "m7i.large")
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{
					getResult: &cloud.Resource{ActualState: actualState},
				},
			},
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.InstanceState != "terminated" {
		t.Fatalf("InstanceState = %q, want terminated", s.InstanceState)
	}
	if s.ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped for terminated instance", s.ProbeStatus)
	}
}

func TestProbeEdgesMissingInstance(t *testing.T) {
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{
					getErr: cloud.ErrResourceNotFound,
				},
			},
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-missing"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.InstanceState != "missing" {
		t.Fatalf("InstanceState = %q, want missing", s.InstanceState)
	}
	if s.ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped for missing instance", s.ProbeStatus)
	}
	if !strings.Contains(s.ProbeError, "Cloud Control Get failed") {
		t.Fatalf("ProbeError = %q, want Cloud Control Get failed", s.ProbeError)
	}
}

func TestProbeEdgesWithRegionError(t *testing.T) {
	provider := &fakeRegionProvider{withRegionErr: cloud.ErrResourceNotFound}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped", s.ProbeStatus)
	}
	if !strings.Contains(s.ProbeError, "cannot reach region") {
		t.Fatalf("ProbeError = %q, want cannot reach region", s.ProbeError)
	}
}

func TestProbeEdgesNilResources(t *testing.T) {
	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {}, // Resources is nil
		},
	}

	edges := []EdgeResource{{Region: "eu-west-1", InstanceID: "i-eu"}}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 1 {
		t.Fatalf("expected 1 status, got %d", len(result))
	}
	s := result[0]
	if s.ProbeStatus != "skipped" {
		t.Fatalf("ProbeStatus = %q, want skipped", s.ProbeStatus)
	}
	if !strings.Contains(s.ProbeError, "no resource client") {
		t.Fatalf("ProbeError = %q, want no resource client", s.ProbeError)
	}
}

func TestProbeEdgesMultipleEdges(t *testing.T) {
	// One running, one stopped, one missing.
	runningState := makeInstanceActualState("running", "10.0.1.1", "m7i.large")
	stoppedState := makeInstanceActualState("stopped", "10.0.2.1", "m5.xlarge")

	provider := &fakeRegionProvider{
		regionViews: map[string]cloud.RegionView{
			"eu-west-1": {
				Resources: &fakeResourceClient{getResult: &cloud.Resource{ActualState: runningState}},
			},
			"ap-southeast-2": {
				Resources: &fakeResourceClient{getResult: &cloud.Resource{ActualState: stoppedState}},
			},
			"us-west-2": {
				Resources: &fakeResourceClient{getErr: cloud.ErrResourceNotFound},
			},
		},
	}

	edges := []EdgeResource{
		{Region: "ap-southeast-2", InstanceID: "i-ap"},
		{Region: "eu-west-1", InstanceID: "i-eu"},
		{Region: "us-west-2", InstanceID: "i-us"},
	}
	result := ProbeEdges(context.Background(), edges, provider, ProbeEdgesOptions{PublicPort: 80})

	if len(result) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(result))
	}
	// ap-southeast-2: stopped
	if result[0].InstanceState != "stopped" || result[0].ProbeStatus != "skipped" {
		t.Fatalf("ap-southeast-2: %+v", result[0])
	}
	// eu-west-1: running, probe attempted
	if result[1].InstanceState != "running" || result[1].ProbeStatus != "unreachable" {
		t.Fatalf("eu-west-1: %+v", result[1])
	}
	// us-west-2: missing
	if result[2].InstanceState != "missing" || result[2].ProbeStatus != "skipped" {
		t.Fatalf("us-west-2: %+v", result[2])
	}
}

func TestProbeEdgeHealth(t *testing.T) {
	client := &http.Client{Timeout: edgeProbeTimeout}

	// Test with a server that returns 200.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, portStr, _ := net.SplitHostPort(server.Listener.Addr().String())
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	result := probeEdgeHealth(client, host, port)
	if result != "ready" {
		t.Fatalf("probeEdgeHealth = %q, want ready", result)
	}

	// Test with a server that returns 500.
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server500.Close()
	host500, portStr500, _ := net.SplitHostPort(server500.Listener.Addr().String())
	var port500 int
	_, _ = fmt.Sscanf(portStr500, "%d", &port500)
	result500 := probeEdgeHealth(client, host500, port500)
	if result500 != "unreachable" {
		t.Fatalf("probeEdgeHealth = %q, want unreachable for 500", result500)
	}
}

func TestParseEdgeActualStateInvalidJSON(t *testing.T) {
	s := EdgeStatus{Region: "eu-west-1", InstanceID: "i-eu"}
	r := &cloud.Resource{ActualState: json.RawMessage(`not valid json`)}
	parseEdgeActualState(r, &s)
	// Should silently ignore invalid JSON.
	if s.InstanceState != "" {
		t.Fatalf("InstanceState should be empty for invalid JSON, got %q", s.InstanceState)
	}
	if s.PrivateIP != "" {
		t.Fatalf("PrivateIP should be empty for invalid JSON, got %q", s.PrivateIP)
	}
}

func TestParseEdgeActualStateNil(t *testing.T) {
	s := EdgeStatus{Region: "eu-west-1", InstanceID: "i-eu"}
	r := &cloud.Resource{ActualState: nil}
	parseEdgeActualState(r, &s)
	if s.InstanceState != "" {
		t.Fatalf("InstanceState should be empty for nil ActualState, got %q", s.InstanceState)
	}
}
