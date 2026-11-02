package drift

// Two-region fake provider tests for DDC edge routing (#529). The fake models
// two AWS regions with separate resource stores: a Get for an identifier that
// does not exist in the calling region's store fails, so any call routed to
// the wrong region surfaces as Missing or Error instead of silently passing.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/state"
)

const (
	homeTestRegion = "us-east-1"
	edgeTestRegion = "eu-west-1"
)

// regionRC is a ResourceClient bound to one region's store.
type regionRC struct {
	fake   *twoRegionFake
	region string
	store  map[string]cloud.Resource
}

func (c *regionRC) Get(_ context.Context, r *cloud.Resource) error {
	c.fake.gets[c.region]++
	live, ok := c.store[r.Identifier]
	if !ok {
		return cloud.ErrResourceNotFound
	}
	r.ActualState = live.ActualState
	return nil
}

func (c *regionRC) List(_ context.Context, typeName string) ([]cloud.Resource, error) {
	var out []cloud.Resource
	for _, r := range c.store {
		if r.TypeName == typeName {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *regionRC) Create(_ context.Context, r *cloud.Resource) error {
	r.Identifier = "i-" + c.region
	c.store[r.Identifier] = *r
	return nil
}

func (c *regionRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }

func (c *regionRC) Delete(_ context.Context, r *cloud.Resource) error {
	delete(c.store, r.Identifier)
	return nil
}

// twoRegionFake holds one store per region and counts Gets per region so
// tests can prove edge reads went through the edge client.
type twoRegionFake struct {
	stores map[string]map[string]cloud.Resource
	gets   map[string]int
}

func newTwoRegionFake() *twoRegionFake {
	return &twoRegionFake{
		stores: map[string]map[string]cloud.Resource{},
		gets:   map[string]int{},
	}
}

func (f *twoRegionFake) storeFor(region string) map[string]cloud.Resource {
	if f.stores[region] == nil {
		f.stores[region] = map[string]cloud.Resource{}
	}
	return f.stores[region]
}

func (f *twoRegionFake) put(region string, r cloud.Resource) {
	f.storeFor(region)[r.Identifier] = r
}

func (f *twoRegionFake) client(region string) cloud.ResourceClient {
	return &regionRC{fake: f, region: region, store: f.storeFor(region)}
}

// regionResource is the Engine.RegionResource seam: it resolves the
// region-scoped client for non-home regions.
func (f *twoRegionFake) regionResource(_ context.Context, region string) (cloud.ResourceClient, error) {
	return f.client(region), nil
}

// twoRegionEngine builds a drift engine whose home client is the fake's
// home-region client and whose edge reads/listing go through the seam.
func twoRegionEngine(st *state.State, f *twoRegionFake) *Engine {
	homeRC := f.client(homeTestRegion)
	return &Engine{
		State:          st,
		ResourceGet:    homeRC.Get,
		ResourceList:   homeRC.List,
		RegionResource: f.regionResource,
		BackendChecker: &fakeBackendChecker{bucketExists: true, tableExists: true},
		Config: &DriftConfig{
			Account: "123456789012",
			Region:  homeTestRegion,
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
	}
}

// ddcEdgeState records a DDC home pair plus one edge pair in eu-west-1 with
// the full post-#529 recorded inputs (vpcId on the SG; imageId/subnetId on
// the instance).
func ddcEdgeState() *state.State {
	st := state.NewState("123456789012", homeTestRegion)
	st.UpsertModule("ddc", "ami-home", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"},
		{
			TypeName:   cloud.TypeAWSEC2Instance,
			Identifier: "i-home",
			Properties: map[string]string{
				"instanceType": "m7i.xlarge",
				"imageId":      "ami-home",
				"subnetId":     "subnet-home",
			},
		},
		{
			TypeName:   cloud.TypeAWSEC2SecurityGroup,
			Identifier: "sg-edge",
			Properties: map[string]string{
				"region": edgeTestRegion,
				"role":   ddc.RoleEdge,
				"vpcId":  "vpc-edge",
			},
		},
		{
			TypeName:   cloud.TypeAWSEC2Instance,
			Identifier: "i-edge",
			Properties: map[string]string{
				"region":       edgeTestRegion,
				"role":         ddc.RoleEdge,
				"instanceType": "m7i.xlarge",
				"volumeSize":   "100",
				"imageId":      "ami-edge",
				"subnetId":     "subnet-edge",
				"vpcId":        "vpc-edge",
			},
		},
	})
	return st
}

// putLiveHome stores a live home-region SG and running home instance.
func putLiveHome(f *twoRegionFake) {
	f.put(homeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"})
	f.put(homeTestRegion, cloud.Resource{
		TypeName:    cloud.TypeAWSEC2Instance,
		Identifier:  "i-home",
		ActualState: json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.xlarge","ImageId":"ami-home"}`),
	})
}

func TestTwoRegion_EdgeResourcesInSync(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	putLiveHome(f)
	f.put(edgeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge"})
	f.put(edgeTestRegion, cloud.Resource{
		TypeName:    cloud.TypeAWSEC2Instance,
		Identifier:  "i-edge",
		ActualState: json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.xlarge","ImageId":"ami-edge"}`),
	})

	report := twoRegionEngine(st, f).Run(context.Background())

	// A wrong-region Get would miss the other region's store and report
	// Missing; all four resources must be in sync.
	if report.Missing != 0 {
		t.Errorf("expected 0 missing, got %d", report.Missing)
	}
	if report.InSync != 4 {
		t.Errorf("expected 4 in-sync, got %d", report.InSync)
	}
	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", report.Errors)
	}
	if f.gets[edgeTestRegion] != 2 {
		t.Errorf("expected 2 edge-region Gets (SG + instance), got %d", f.gets[edgeTestRegion])
	}
	if f.gets[homeTestRegion] != 2 {
		t.Errorf("expected 2 home Gets, got %d", f.gets[homeTestRegion])
	}
}

func TestTwoRegion_NilRegionResourceReportsError(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	putLiveHome(f)

	engine := twoRegionEngine(st, f)
	engine.RegionResource = nil

	report := engine.Run(context.Background())
	edge := findResource(report, "i-edge")
	if edge == nil {
		t.Fatal("no drift result for i-edge")
	}
	if edge.Status != Error {
		t.Errorf("expected Error status for edge resource without region support, got %s", edge.Status)
	}
	if !strings.Contains(edge.Details, "provider does not support additional regions") {
		t.Errorf("expected region-support error in details, got %q", edge.Details)
	}

	plan := PlanRemediation(report, st, config.Defaults())
	edgePlan := findAction(plan, "i-edge")
	if edgePlan == nil || edgePlan.Kind != ActionSkip {
		t.Fatalf("expected i-edge ActionSkip, got %+v", edgePlan)
	}
	if !strings.Contains(edgePlan.Reason, "cannot remediate: failed to query resource: routing to region "+edgeTestRegion) {
		t.Errorf("expected routing failure in skip reason, got %q", edgePlan.Reason)
	}
}

func TestTwoRegion_StrayEdgeInstanceIsExtra(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	putLiveHome(f)
	f.put(edgeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge"})
	f.put(edgeTestRegion, cloud.Resource{
		TypeName:    cloud.TypeAWSEC2Instance,
		Identifier:  "i-edge",
		ActualState: json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.xlarge","ImageId":"ami-edge"}`),
	})
	// A stray instance in the edge store that is not recorded anywhere.
	f.put(edgeTestRegion, cloud.Resource{
		TypeName:    cloud.TypeAWSEC2Instance,
		Identifier:  "i-stray",
		ActualState: json.RawMessage(`{"State":{"Name":"running"}}`),
	})

	report := twoRegionEngine(st, f).Run(context.Background())
	if report.Extra != 1 {
		t.Fatalf("expected 1 extra, got %d", report.Extra)
	}
	for i := range report.Modules {
		for j := range report.Modules[i].Resources {
			r := &report.Modules[i].Resources[j]
			if r.Identifier != "i-stray" || r.Status != Extra {
				continue
			}
			if r.Module != "ddc" {
				t.Errorf("expected stray owned by ddc, got %s", r.Module)
			}
		}
	}
}

func findResource(report *DriftReport, identifier string) *DriftResult {
	for i := range report.Modules {
		for j := range report.Modules[i].Resources {
			if report.Modules[i].Resources[j].Identifier == identifier {
				return &report.Modules[i].Resources[j]
			}
		}
	}
	return nil
}

func findAction(plan *RemediationPlan, identifier string) *RemediationAction {
	for i := range plan.Actions {
		if plan.Actions[i].Identifier == identifier {
			return &plan.Actions[i]
		}
	}
	return nil
}
