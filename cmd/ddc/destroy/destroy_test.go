package destroy

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestResourceOrder(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
			{TypeName: ddc.TypeAWSIAMRole, Identifier: "role"},
			{TypeName: ddc.TypeAWSIAMInstanceProfile, Identifier: "prof"},
			{TypeName: ddc.TypeAWSS3Bucket, Identifier: "bucket"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-scylla", Properties: map[string]string{"role": ddc.RoleScylla}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		},
	}
	got := ResourceOrder(m)
	if len(got) != 6 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Identifier != "i-ddc" {
		t.Fatalf("first = %s, want coordinator", got[0].Identifier)
	}
	if got[1].Identifier != "i-scylla" {
		t.Fatalf("second = %s, want scylla", got[1].Identifier)
	}
	if got[2].Identifier != "bucket" {
		t.Fatalf("third = %s", got[2].Identifier)
	}
}

func TestNewTeardownRun(t *testing.T) {
	provider := &testutil.TestProvider{}
	st := &fabricastate.State{Account: "123"}
	st.UpsertModule("ddc", "ami", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-1", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
	})
	rt := globals.Runtime{Config: &config.Config{}, Provider: provider}
	var buf bytes.Buffer
	tc := NewTeardown(rt, &buf)
	tc.ReadState = func() (*fabricastate.State, error) { return st, nil }
	tc.WriteState = func(*fabricastate.State) error { return nil }
	resources := provider.Resources()
	tc.DeleteResource = wrapDelete(resources.Delete)
	tc.GetResource = resources.Get
	if err := tc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.DeleteCalls == 0 {
		t.Fatal("expected deletes")
	}
}

func TestNewCobra(t *testing.T) {
	cmd := New(func() (globals.Runtime, error) {
		return globals.Runtime{Config: &config.Config{}}, nil
	}, func() globals.Options { return globals.Options{DryRun: true} }, io.Discard)
	if cmd.Use != "destroy" {
		t.Fatalf("Use = %s", cmd.Use)
	}
}

func multiRegionModule() *fabricastate.ModuleState {
	return &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"},
			{TypeName: ddc.TypeAWSIAMRole, Identifier: "role"},
			{TypeName: ddc.TypeAWSIAMInstanceProfile, Identifier: "prof"},
			{TypeName: ddc.TypeAWSS3Bucket, Identifier: "bucket"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-scylla", Properties: map[string]string{"role": ddc.RoleScylla}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: map[string]string{"role": ddc.RoleCoordinator}},
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": ddc.RoleEdge}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ap", Properties: map[string]string{"region": "ap-southeast-2", "role": ddc.RoleEdge}},
		},
	}
}

func TestCollectEdgesSorted(t *testing.T) {
	edges := CollectEdges(multiRegionModule())
	if len(edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(edges))
	}
	if edges[0].region != "ap-southeast-2" || edges[0].instanceID != "i-ap" || edges[0].sgID != "sg-ap" {
		t.Fatalf("edges[0] = %+v", edges[0])
	}
	if edges[1].region != "eu-west-1" || edges[1].instanceID != "i-eu" {
		t.Fatalf("edges[1] = %+v", edges[1])
	}
}

func TestResourceOrderWithEdges(t *testing.T) {
	got := ResourceOrder(multiRegionModule())
	want := []string{"i-ap", "sg-ap", "i-eu", "sg-eu", "i-ddc", "i-scylla", "bucket", "prof", "role", "sg-home"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].Identifier != id {
			t.Fatalf("order[%d] = %s, want %s; full: %v", i, got[i].Identifier, id, got)
		}
	}
}

// legacyProvider implements cloud.Provider but not cloud.RegionProvider.
type legacyProvider struct{ inner *testutil.TestProvider }

func (l legacyProvider) Name() string { return l.inner.Name() }
func (l legacyProvider) Identity(ctx context.Context) (string, string, string, error) {
	return l.inner.Identity(ctx)
}
func (l legacyProvider) Resources() cloud.ResourceClient { return l.inner.Resources() }

func TestDeleteWithRegionsNoRegionProvider(t *testing.T) {
	st := &fabricastate.State{Account: "123"}
	st.UpsertModule("ddc", "ami", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-eu", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-eu", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
	})
	var buf bytes.Buffer
	rt := globals.Runtime{Config: &config.Config{}, Provider: legacyProvider{inner: &testutil.TestProvider{}}}
	tc := NewTeardown(rt, &buf)
	tc.ReadState = func() (*fabricastate.State, error) { return st, nil }
	tc.WriteState = func(*fabricastate.State) error { return nil }
	err := tc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot delete edge resources") {
		t.Fatalf("err = %v, want region-provider failure", err)
	}
}

// regionTrackingProvider records which region each delete went to via a
// per-region ResourceClient that tags deleted identifiers.
type regionTrackingProvider struct {
	*testutil.TestProvider
	deleted []string
}

func (p *regionTrackingProvider) WithRegion(_ context.Context, region string) (cloud.RegionView, error) {
	return cloud.RegionView{
		Resources: &regionTagClient{inner: p.TestProvider.Resources().(*testutil.FakeResourceClient), p: p, region: region},
		VPCs:      &testutil.TestVPCResolver{},
	}, nil
}

type regionTagClient struct {
	inner  *testutil.FakeResourceClient
	p      *regionTrackingProvider
	region string
}

func (r *regionTagClient) Delete(ctx context.Context, res *cloud.Resource) error {
	r.p.deleted = append(r.p.deleted, r.region+":"+res.Identifier)
	return r.inner.Delete(ctx, res)
}

func (r *regionTagClient) Get(ctx context.Context, res *cloud.Resource) error {
	return r.inner.Get(ctx, res)
}
func (r *regionTagClient) Create(ctx context.Context, res *cloud.Resource) error {
	return r.inner.Create(ctx, res)
}
func (r *regionTagClient) Update(ctx context.Context, res *cloud.Resource) error {
	return r.inner.Update(ctx, res)
}
func (r *regionTagClient) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return r.inner.List(ctx, typeName)
}

func TestDeleteWithRegions(t *testing.T) {
	fp := &regionTrackingProvider{TestProvider: &testutil.TestProvider{}}
	st := &fabricastate.State{Account: "123"}
	st.UpsertModule("ddc", "ami", "ready", multiRegionModule().Resources)
	var buf bytes.Buffer
	rt := globals.Runtime{Config: &config.Config{}, Provider: fp}
	tc := NewTeardown(rt, &buf)
	tc.ReadState = func() (*fabricastate.State, error) { return st, nil }
	tc.WriteState = func(*fabricastate.State) error { return nil }
	if err := tc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// All 10 resources deleted; edges routed to their region.
	want := []string{
		"ap-southeast-2:i-ap", "ap-southeast-2:sg-ap",
		"eu-west-1:i-eu", "eu-west-1:sg-eu",
	}
	got := fp.deleted
	if len(got) != 4 {
		t.Fatalf("region-tagged deletes = %d, want 4: %v", len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("deleted[%d] = %s, want %s", i, got[i], w)
		}
	}
	// Home resources (6) also deleted via the default untagged client;
	// total across all clients is 10.
	if fp.DeleteCalls != 10 {
		t.Fatalf("total deletes = %d, want 10", fp.DeleteCalls)
	}
	// Module must be fully removed after a successful destroy.
	if st.GetModule("ddc") != nil {
		t.Fatal("module not removed from state")
	}
}
