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
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"},
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

// putLiveHome stores the live home-region SG, IAM profile, and running home
// instance.
func putLiveHome(f *twoRegionFake) {
	f.put(homeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"})
	f.put(homeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"})
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
	// Missing; all five resources must be in sync.
	if report.Missing != 0 {
		t.Errorf("expected 0 missing, got %d", report.Missing)
	}
	if report.InSync != 5 {
		t.Errorf("expected 5 in-sync, got %d", report.InSync)
	}
	if report.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", report.Errors)
	}
	if f.gets[edgeTestRegion] != 2 {
		t.Errorf("expected 2 edge-region Gets (SG + instance), got %d", f.gets[edgeTestRegion])
	}
	if f.gets[homeTestRegion] != 3 {
		t.Errorf("expected 3 home Gets (SG + profile + instance), got %d", f.gets[homeTestRegion])
	}
}

func TestTwoRegion_EdgeInstanceMissingRemediation(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	putLiveHome(f)
	f.put(edgeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge"})
	// i-edge is absent from the edge store: only the edge instance is missing.

	report := twoRegionEngine(st, f).Run(context.Background())
	if report.Missing != 1 {
		t.Fatalf("expected 1 missing, got %d", report.Missing)
	}

	cfg := config.Defaults()
	plan := PlanRemediation(report, st, cfg)
	if plan.ToFix != 1 {
		t.Fatalf("expected 1 fix action, got %d (skipped: %d)", plan.ToFix, plan.ToSkip)
	}
	action := plan.Actions[0]
	if action.Kind != ActionCreate {
		t.Fatalf("expected ActionCreate, got %s", action.Kind)
	}
	if action.Region != edgeTestRegion {
		t.Errorf("expected action region %s, got %q", edgeTestRegion, action.Region)
	}
	if action.Identifier != "i-edge" {
		t.Errorf("expected i-edge, got %s", action.Identifier)
	}

	// The rebuilt desired state must use the recorded edge inputs, not the
	// home config defaults.
	var ds map[string]any
	if err := json.Unmarshal(action.Resource.DesiredState, &ds); err != nil {
		t.Fatalf("invalid desired state: %v", err)
	}
	if ds["ImageId"] != "ami-edge" {
		t.Errorf("expected recorded edge AMI ami-edge, got %v", ds["ImageId"])
	}
	if ds["SubnetId"] != "subnet-edge" {
		t.Errorf("expected recorded edge subnet subnet-edge, got %v", ds["SubnetId"])
	}
	sgIDs, ok := ds["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0].(string) != "sg-edge" {
		t.Errorf("expected edge SG sg-edge in desired state, got %v", ds["SecurityGroupIds"])
	}

	// Apply routes the create through the edge client via the action region.
	var createdIn []string
	seam := func(_ context.Context, a *RemediationAction) error {
		createdIn = append(createdIn, a.Region)
		return f.client(a.Region).Create(context.Background(), a.Resource)
	}
	result := ApplyRemediation(context.Background(), plan, st, seam)
	if len(result.Applied) != 1 {
		t.Fatalf("expected 1 applied, got %d (errors: %v)", len(result.Applied), result.Errors)
	}
	if len(createdIn) != 1 || createdIn[0] != edgeTestRegion {
		t.Errorf("expected create routed to %s, got %v", edgeTestRegion, createdIn)
	}
	if _, ok := f.storeFor(edgeTestRegion)[action.Resource.Identifier]; !ok {
		t.Errorf("expected the new instance in the %s store", edgeTestRegion)
	}
}

func TestTwoRegion_PreRecordingEdgeInstanceSkips(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	// Strip the inputs recorded since #529: pre-existing state has only
	// region/role/instanceType/volumeSize on the edge instance.
	mod := st.GetModule("ddc")
	for i := range mod.Resources {
		if mod.Resources[i].Identifier == "i-edge" {
			delete(mod.Resources[i].Properties, "imageId")
			delete(mod.Resources[i].Properties, "subnetId")
		}
	}
	putLiveHome(f)
	f.put(edgeTestRegion, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge"})

	report := twoRegionEngine(st, f).Run(context.Background())
	plan := PlanRemediation(report, st, config.Defaults())
	if plan.ToFix != 0 || plan.ToSkip != 1 {
		t.Fatalf("expected 0 fix / 1 skip, got %d fix / %d skip", plan.ToFix, plan.ToSkip)
	}
	action := plan.Actions[0]
	if action.Kind != ActionSkip {
		t.Errorf("expected ActionSkip, got %s", action.Kind)
	}
	if !strings.Contains(action.Reason, "fabrica ddc region add "+edgeTestRegion) {
		t.Errorf("expected re-provisioning hint in skip reason, got %q", action.Reason)
	}
}

func TestTwoRegion_PreRecordingEdgeSGSkips(t *testing.T) {
	f := newTwoRegionFake()
	st := ddcEdgeState()
	mod := st.GetModule("ddc")
	for i := range mod.Resources {
		if mod.Resources[i].Identifier == "sg-edge" {
			delete(mod.Resources[i].Properties, "vpcId")
		}
	}
	putLiveHome(f)
	// The edge store is empty: the edge SG is missing.

	report := twoRegionEngine(st, f).Run(context.Background())
	plan := PlanRemediation(report, st, config.Defaults())
	// The edge instance is also missing but rebuildable; only the SG skips.
	var sgAction *RemediationAction
	for i := range plan.Actions {
		if plan.Actions[i].Identifier == "sg-edge" {
			sgAction = &plan.Actions[i]
		}
	}
	if sgAction == nil {
		t.Fatalf("no action recorded for sg-edge: %v", plan.Actions)
	}
	if sgAction.Kind != ActionSkip {
		t.Fatalf("expected sg-edge ActionSkip, got %s", sgAction.Kind)
	}
	if !strings.Contains(sgAction.Reason, "fabrica ddc region add "+edgeTestRegion) {
		t.Errorf("expected re-provisioning hint in skip reason, got %q", sgAction.Reason)
	}
	if sgAction.Region != edgeTestRegion {
		t.Errorf("expected action region %s, got %q", edgeTestRegion, sgAction.Region)
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

// --- refreshSGIDs region guard ---

func TestRefreshSGIDs_RegionGuard(t *testing.T) {
	sgDS := json.RawMessage(`{"GroupDescription":"test"}`)
	homeInst := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-home-old"]}`)
	edgeInst := json.RawMessage(`{"InstanceType":"m5.xlarge","SecurityGroupIds":["sg-edge-old"]}`)

	plan := &RemediationPlan{
		Actions: []RemediationAction{
			{Module: "ddc", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home-old", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home-new", DesiredState: sgDS}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, DesiredState: homeInst}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge-old", Region: edgeTestRegion, Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge-new", DesiredState: sgDS}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge", Region: edgeTestRegion, Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, DesiredState: edgeInst}},
			{Module: "ddc", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-home-2", Kind: ActionCreate, Resource: &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, DesiredState: homeInst}},
		},
	}

	// Home SG refresh rewires only home-region instance actions.
	refreshSGIDs(plan, 0, "ddc", "", "sg-home-old", "sg-home-new")
	if got := sgIDsIn(t, plan.Actions[1].Resource.DesiredState); got != "sg-home-new" {
		t.Errorf("home instance = %s, want sg-home-new", got)
	}
	if got := sgIDsIn(t, plan.Actions[3].Resource.DesiredState); got != "sg-edge-old" {
		t.Errorf("edge instance rewired by home SG refresh: %s", got)
	}
	if got := sgIDsIn(t, plan.Actions[4].Resource.DesiredState); got != "sg-home-new" {
		t.Errorf("second home instance = %s, want sg-home-new", got)
	}

	// Edge SG refresh rewires only the edge instance.
	refreshSGIDs(plan, 2, "ddc", edgeTestRegion, "sg-edge-old", "sg-edge-new")
	if got := sgIDsIn(t, plan.Actions[3].Resource.DesiredState); got != "sg-edge-new" {
		t.Errorf("edge instance = %s, want sg-edge-new", got)
	}
	if got := sgIDsIn(t, plan.Actions[4].Resource.DesiredState); got != "sg-home-new" {
		t.Errorf("home instance rewired by edge SG refresh: %s", got)
	}
}

// --- rebuildEdgeInstanceDesiredState direct tests ---

func TestRebuildEdgeInstanceDesiredState_Full(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("123456789012", homeTestRegion)
	mod := &state.ModuleState{Name: "ddc"}
	mod.Resources = []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"},
		{
			TypeName:   cloud.TypeAWSEC2SecurityGroup,
			Identifier: "sg-edge",
			Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge},
		},
		{
			TypeName:   cloud.TypeAWSEC2Instance,
			Identifier: "i-edge",
			Properties: map[string]string{
				"region":       edgeTestRegion,
				"role":         ddc.RoleEdge,
				"instanceType": "m7i.4xlarge",
				"imageId":      "ami-edge",
				"subnetId":     "subnet-edge",
			},
		},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"},
	}
	rec := &mod.Resources[2]
	ds, err := rebuildEdgeInstanceDesiredState(rec, mod, st, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m7i.4xlarge" {
		t.Errorf("expected recorded instance type, got %v", m["InstanceType"])
	}
	if m["ImageId"] != "ami-edge" || m["SubnetId"] != "subnet-edge" {
		t.Errorf("expected recorded edge AMI/subnet, got %v / %v", m["ImageId"], m["SubnetId"])
	}
	sgIDs, ok := m["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 || sgIDs[0].(string) != "sg-edge" {
		t.Errorf("expected the edge-region SG sg-edge, got %v", m["SecurityGroupIds"])
	}
	if m["IamInstanceProfile"] != "fabrica-ddc-profile" {
		t.Errorf("expected the recorded IAM profile, got %v", m["IamInstanceProfile"])
	}
}

func TestRebuildEdgeInstanceDesiredState_TypeFallback(t *testing.T) {
	st := state.NewState("123456789012", homeTestRegion)
	mod := &state.ModuleState{Name: "ddc", Resources: []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge", Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge}},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"},
	}}
	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge, "imageId": "ami-edge", "subnetId": "subnet-edge"},
	}

	cfg := config.Defaults()
	cfg.DDC.InstanceType = "m7i.2xlarge"
	ds, err := rebuildEdgeInstanceDesiredState(rec, mod, st, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m7i.2xlarge" {
		t.Errorf("expected config instance type fallback, got %v", m["InstanceType"])
	}

	cfg2 := config.Defaults()
	ds, err = rebuildEdgeInstanceDesiredState(rec, mod, st, cfg2)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != ddc.DefaultInstanceType {
		t.Errorf("expected default %s, got %v", ddc.DefaultInstanceType, m["InstanceType"])
	}
}

func TestRebuildEdgeInstanceDesiredState_MissingInputs(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("123456789012", homeTestRegion)
	mod := &state.ModuleState{Name: "ddc", Resources: []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge", Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge}},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"},
	}}

	noAMI := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge, "subnetId": "subnet-edge"},
	}
	if _, err := rebuildEdgeInstanceDesiredState(noAMI, mod, st, cfg); err == nil {
		t.Error("expected error without recorded AMI")
	} else if !strings.Contains(err.Error(), "no AMI recorded") {
		t.Errorf("unexpected error: %v", err)
	}

	noSubnet := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge, "imageId": "ami-edge"},
	}
	if _, err := rebuildEdgeInstanceDesiredState(noSubnet, mod, st, cfg); err == nil {
		t.Error("expected error without recorded subnet")
	} else if !strings.Contains(err.Error(), "no subnet recorded") {
		t.Errorf("unexpected error: %v", err)
	}

	// The edge instance keeps its recorded inputs but the module recorded no
	// edge security group in the edge region.
	noSG := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2Instance,
		Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge, "imageId": "ami-edge", "subnetId": "subnet-edge"},
	}
	noSGMod := &state.ModuleState{Name: "ddc", Resources: []state.ModuleResource{
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-ddc-profile"},
	}}
	if _, err := rebuildEdgeInstanceDesiredState(noSG, noSGMod, st, cfg); err == nil {
		t.Error("expected error without a recorded edge security group")
	} else if !strings.Contains(err.Error(), "no security group recorded") {
		t.Errorf("unexpected error: %v", err)
	}

	// A home security group alone does not satisfy an edge rebuild: the
	// profile is the remaining required input.
	edgeSGOnly := &state.ModuleState{Name: "ddc", Resources: []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge", Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge}},
	}}
	if _, err := rebuildEdgeInstanceDesiredState(noSG, edgeSGOnly, st, cfg); err == nil {
		t.Error("expected error without a recorded IAM instance profile")
	} else if !strings.Contains(err.Error(), "no IAM instance profile recorded") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRebuildDDCSG_EdgeWithoutVPCFails(t *testing.T) {
	cfg := config.Defaults()
	rec := &state.ModuleResource{
		TypeName:   cloud.TypeAWSEC2SecurityGroup,
		Properties: map[string]string{"region": edgeTestRegion, "role": ddc.RoleEdge},
	}
	_, err := rebuildDDCSG(rec, cfg)
	if err == nil {
		t.Fatal("expected error for edge SG without recorded VPC")
	}
	if !strings.Contains(err.Error(), "fabrica ddc region add "+edgeTestRegion) {
		t.Errorf("expected re-provisioning hint, got %v", err)
	}
}

// --- helpers ---

func sgIDsIn(t *testing.T, ds json.RawMessage) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid desired state: %v", err)
	}
	sgIDs, ok := m["SecurityGroupIds"].([]any)
	if !ok || len(sgIDs) != 1 {
		t.Fatalf("expected 1 SecurityGroupIds entry, got %v", m["SecurityGroupIds"])
	}
	return sgIDs[0].(string)
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
