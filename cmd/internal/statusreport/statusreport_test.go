package statusreport

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestBuildStatusReport_EmptyState(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-east-1",
				},
			},
		},
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if report.Summary.ModuleCount != 0 {
		t.Errorf("moduleCount = %d, want 0", report.Summary.ModuleCount)
	}
	if report.Backend.BucketExists != "unknown" {
		t.Errorf("bucketExists = %q, want unknown", report.Backend.BucketExists)
	}
}

func TestBuildStatusReport_WithModules(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
		{
			Name:   "horde",
			Status: "provisioning",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-456"},
			},
		},
	}
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-east-1",
				},
			},
		},
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if report.Summary.ModuleCount != 2 {
		t.Errorf("moduleCount = %d, want 2", report.Summary.ModuleCount)
	}
	if report.Summary.Healthy != 1 {
		t.Errorf("healthy = %d, want 1", report.Summary.Healthy)
	}
	if report.Summary.Provisioning != 1 {
		t.Errorf("provisioning = %d, want 1", report.Summary.Provisioning)
	}
	if report.Summary.ResourceCount != 3 {
		t.Errorf("resourceCount = %d, want 3", report.Summary.ResourceCount)
	}
}

func TestBuildStatusReport_ModuleFields(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:    "perforce",
			Status:  "ready",
			Version: "p4-2024.1",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
	}
	rt := globals.Runtime{Config: &config.Config{}}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{})

	if len(report.Modules) != 1 {
		t.Fatalf("modules count = %d, want 1", len(report.Modules))
	}
	m := report.Modules[0]
	if m.Name != "perforce" {
		t.Errorf("name = %q, want perforce", m.Name)
	}
	if m.Status != "ready" {
		t.Errorf("status = %q, want ready", m.Status)
	}
	if m.Version != "p4-2024.1" {
		t.Errorf("version = %q, want p4-2024.1", m.Version)
	}
	if m.ResourceCount != 2 {
		t.Errorf("resourceCount = %d, want 2", m.ResourceCount)
	}
	if m.SGID != "sg-123" {
		t.Errorf("sgId = %q, want sg-123", m.SGID)
	}
	if m.InstanceID != "i-abc" {
		t.Errorf("instanceId = %q, want i-abc", m.InstanceID)
	}
}

func TestSummaryLine(t *testing.T) {
	tests := []struct {
		name    string
		summary StatusSummary
		wantSub string
	}{
		{
			name:    "empty",
			summary: StatusSummary{},
			wantSub: "No modules provisioned",
		},
		{
			name: "one module",
			summary: StatusSummary{
				ModuleCount:   1,
				ResourceCount: 2,
				Healthy:       1,
			},
			wantSub: "1 module",
		},
		{
			name: "multiple modules",
			summary: StatusSummary{
				ModuleCount:   3,
				ResourceCount: 7,
				Healthy:       2,
				Provisioning:  1,
			},
			wantSub: "3 modules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SummaryLine(tt.summary)
			if !strings.Contains(got, tt.wantSub) {
				t.Errorf("SummaryLine() = %q, want to contain %q", got, tt.wantSub)
			}
		})
	}
}

func TestNextSteps(t *testing.T) {
	modules := []StatusModule{
		{Name: "perforce", Status: "ready"},
		{Name: "horde", Status: "provisioning"},
	}

	steps := NextSteps(modules)

	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	if !strings.Contains(steps[0], "horde") {
		t.Errorf("step = %q, want horde provisioning step", steps[0])
	}
}

func TestNextSteps_None(t *testing.T) {
	modules := []StatusModule{
		{Name: "perforce", Status: "ready"},
	}

	steps := NextSteps(modules)

	if len(steps) != 0 {
		t.Errorf("got %d steps, want 0", len(steps))
	}
}

func TestParseInstanceState(t *testing.T) {
	raw := []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.1.5"}`)
	state, ip := parseInstanceState(raw)
	if state != "running" {
		t.Errorf("state = %q, want running", state)
	}
	if ip != "10.0.1.5" {
		t.Errorf("ip = %q, want 10.0.1.5", ip)
	}
}

func TestParseInstanceState_Empty(t *testing.T) {
	state, ip := parseInstanceState(nil)
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
	if ip != "" {
		t.Errorf("ip = %q, want empty", ip)
	}
}

func TestYesNo(t *testing.T) {
	if yesNo(true, nil) != "yes" {
		t.Error("yesNo(true, nil) should be yes")
	}
	if yesNo(false, nil) != "no" {
		t.Error("yesNo(false, nil) should be no")
	}
	if yesNo(false, context.DeadlineExceeded) != "unknown" {
		t.Error("yesNo(false, err) should be unknown")
	}
}

// — Fakes —

type fakeResourceClient struct {
	getFunc func(ctx context.Context, r *cloud.Resource) error
}

func (f *fakeResourceClient) Get(ctx context.Context, r *cloud.Resource) error {
	return f.getFunc(ctx, r)
}

func (f *fakeResourceClient) Create(ctx context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeResourceClient) Update(ctx context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeResourceClient) Delete(ctx context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeResourceClient) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return nil, nil
}

type fakeProvider struct {
	resources    *fakeResourceClient
	bucketExists bool
	tableExists  bool
	bucketErr    bool
	tableErr     bool
}

func (f *fakeProvider) Name() string {
	return "aws"
}

func (f *fakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-east-1", nil
}

func (f *fakeProvider) Resources() cloud.ResourceClient {
	return f.resources
}

func (f *fakeProvider) StateBucketExists(_ context.Context, _ string) (bool, error) {
	if f.bucketErr {
		return false, context.DeadlineExceeded
	}
	return f.bucketExists, nil
}

func (f *fakeProvider) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	if f.tableErr {
		return false, context.DeadlineExceeded
	}
	return f.tableExists, nil
}

// — checkBackend coverage —

func TestCheckBackend_NilConfig(t *testing.T) {
	rt := globals.Runtime{}
	b := checkBackend(context.Background(), rt)
	if b.BucketExists != "unknown" {
		t.Errorf("bucketExists = %q, want unknown", b.BucketExists)
	}
	if b.TableExists != "unknown" {
		t.Errorf("tableExists = %q, want unknown", b.TableExists)
	}
}

func TestCheckBackend_NoProvider(t *testing.T) {
	rt := globals.Runtime{
		Config: &config.Config{
			State: config.State{
				Bucket: "my-bucket",
				Table:  "my-table",
			},
		},
	}
	b := checkBackend(context.Background(), rt)
	if b.Bucket != "my-bucket" {
		t.Errorf("bucket = %q, want my-bucket", b.Bucket)
	}
	if b.BucketExists != "unknown" {
		t.Errorf("bucketExists = %q, want unknown (no provider)", b.BucketExists)
	}
}

func TestCheckBackend_BucketExists(t *testing.T) {
	fp := &fakeProvider{
		bucketExists: true,
		tableExists:  true,
	}
	rt := globals.Runtime{
		Config: &config.Config{
			State: config.State{
				Bucket: "my-bucket",
				Table:  "my-table",
			},
		},
		Provider: fp,
	}
	b := checkBackend(context.Background(), rt)
	if b.BucketExists != "yes" {
		t.Errorf("bucketExists = %q, want yes", b.BucketExists)
	}
	if b.TableExists != "yes" {
		t.Errorf("tableExists = %q, want yes", b.TableExists)
	}
}

func TestCheckBackend_BucketNotFound(t *testing.T) {
	fp := &fakeProvider{
		bucketExists: false,
		tableExists:  false,
	}
	rt := globals.Runtime{
		Config: &config.Config{
			State: config.State{
				Bucket: "my-bucket",
				Table:  "my-table",
			},
		},
		Provider: fp,
	}
	b := checkBackend(context.Background(), rt)
	if b.BucketExists != "no" {
		t.Errorf("bucketExists = %q, want no", b.BucketExists)
	}
	if b.TableExists != "no" {
		t.Errorf("tableExists = %q, want no", b.TableExists)
	}
}

func TestCheckBackend_CheckError(t *testing.T) {
	fp := &fakeProvider{
		bucketErr: true,
		tableErr:  true,
	}
	rt := globals.Runtime{
		Config: &config.Config{
			State: config.State{
				Bucket: "my-bucket",
				Table:  "my-table",
			},
		},
		Provider: fp,
	}
	b := checkBackend(context.Background(), rt)
	if b.BucketExists != "unknown" {
		t.Errorf("bucketExists = %q, want unknown (error)", b.BucketExists)
	}
	if b.TableExists != "unknown" {
		t.Errorf("tableExists = %q, want unknown (error)", b.TableExists)
	}
}

// — buildModules with provider —

func TestBuildModules_WithProvider(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
	}

	ec2Raw, _ := json.Marshal(map[string]any{
		"State":            map[string]string{"Name": "running"},
		"PrivateIpAddress": "10.0.1.5",
	})

	fp := &fakeProvider{
		resources: &fakeResourceClient{
			getFunc: func(_ context.Context, r *cloud.Resource) error {
				r.ActualState = ec2Raw
				return nil
			},
		},
	}

	rt := globals.Runtime{
		Config:   &config.Config{},
		Provider: fp,
	}

	modules := buildModules(context.Background(), st, rt, BuildOptions{})
	if len(modules) != 1 {
		t.Fatalf("got %d modules, want 1", len(modules))
	}
	m := modules[0]
	if m.InstanceState != "running" {
		t.Errorf("instanceState = %q, want running", m.InstanceState)
	}
	if m.SGID != "sg-123" {
		t.Errorf("sgId = %q, want sg-123", m.SGID)
	}
	if m.InstanceID != "i-abc" {
		t.Errorf("instanceId = %q, want i-abc", m.InstanceID)
	}
}

func TestBuildModules_ResourceNotFound(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-gone"},
			},
		},
	}

	fp := &fakeProvider{
		resources: &fakeResourceClient{
			getFunc: func(_ context.Context, r *cloud.Resource) error {
				return cloud.ErrResourceNotFound
			},
		},
	}

	rt := globals.Runtime{
		Config:   &config.Config{},
		Provider: fp,
	}

	modules := buildModules(context.Background(), st, rt, BuildOptions{})
	if len(modules) != 1 {
		t.Fatalf("got %d modules, want 1", len(modules))
	}
	if modules[0].InstanceState != "" {
		t.Errorf("instanceState = %q, want empty (not found)", modules[0].InstanceState)
	}
}

// — liveInstance coverage —

func TestLiveInstance_NilGetResource(t *testing.T) {
	state, _ := liveInstance(context.Background(), "i-abc", nil)
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
}

func TestLiveInstance_EmptyID(t *testing.T) {
	state, _ := liveInstance(context.Background(), "", func(_ context.Context, r *cloud.Resource) error {
		return nil
	})
	if state != "" {
		t.Errorf("state = %q, want empty", state)
	}
}

func TestLiveInstance_GetError(t *testing.T) {
	state, _ := liveInstance(context.Background(), "i-abc", func(_ context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	})
	if state != "" {
		t.Errorf("state = %q, want empty (error)", state)
	}
}

// — parseInstanceState bad JSON —

func TestParseInstanceState_BadJSON(t *testing.T) {
	state, ip := parseInstanceState([]byte(`not json`))
	if state != "" {
		t.Errorf("state = %q, want empty (bad json)", state)
	}
	if ip != "" {
		t.Errorf("ip = %q, want empty (bad json)", ip)
	}
}

// — probeModule coverage —

func TestProbeModule_UnknownModule(t *testing.T) {
	result := probeModule("unknown_module", "10.0.1.5", func(_ string) bool {
		return true
	})
	if result != "skipped (no known port)" {
		t.Errorf("probeModule = %q, want skipped (no known port)", result)
	}
}

func TestProbeModule_Responding(t *testing.T) {
	result := probeModule("perforce", "10.0.1.5", func(addr string) bool {
		if addr != "10.0.1.5:1666" {
			t.Errorf("probe addr = %q, want 10.0.1.5:1666", addr)
		}
		return true
	})
	if result != "responding" {
		t.Errorf("probeModule = %q, want responding", result)
	}
}

func TestProbeModule_Unreachable(t *testing.T) {
	result := probeModule("perforce", "10.0.1.5", func(_ string) bool {
		return false
	})
	if result != "unreachable" {
		t.Errorf("probeModule = %q, want unreachable", result)
	}
}

// — BuildStatusReport with probe —

func TestBuildStatusReport_WithProbe(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
			},
		},
	}

	ec2Raw, _ := json.Marshal(map[string]any{
		"State":            map[string]string{"Name": "running"},
		"PrivateIpAddress": "10.0.1.5",
	})

	fp := &fakeProvider{
		resources: &fakeResourceClient{
			getFunc: func(_ context.Context, r *cloud.Resource) error {
				r.ActualState = ec2Raw
				return nil
			},
		},
	}

	rt := globals.Runtime{
		Config:   &config.Config{},
		Provider: fp,
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{
		Probe: true,
		ProbeTCP: func(_ string) bool {
			return true
		},
	})

	if len(report.Modules) != 1 {
		t.Fatalf("got %d modules, want 1", len(report.Modules))
	}
	if report.Modules[0].Probe != "responding" {
		t.Errorf("probe = %q, want responding", report.Modules[0].Probe)
	}
}

func TestBuildStatusReport_ProbeNoAddress(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{
		{
			Name:   "perforce",
			Status: "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-gone"},
			},
		},
	}

	fp := &fakeProvider{
		resources: &fakeResourceClient{
			getFunc: func(_ context.Context, r *cloud.Resource) error {
				return cloud.ErrResourceNotFound
			},
		},
	}

	rt := globals.Runtime{
		Config:   &config.Config{},
		Provider: fp,
	}

	report := BuildStatusReport(context.Background(), st, rt, BuildOptions{
		Probe:    true,
		ProbeTCP: func(_ string) bool { return true },
	})

	if len(report.Modules) != 1 {
		t.Fatalf("got %d modules, want 1", len(report.Modules))
	}
	if report.Modules[0].Probe != "skipped (no reachable address)" {
		t.Errorf("probe = %q, want skipped (no reachable address)", report.Modules[0].Probe)
	}
}

// — SummaryLine edge cases —

func TestSummaryLine_NoModules(t *testing.T) {
	s := StatusSummary{}
	line := SummaryLine(s)
	if line != "No modules provisioned" {
		t.Errorf("SummaryLine = %q, want 'No modules provisioned'", line)
	}
}

func TestSummaryLine_SingleModule(t *testing.T) {
	s := StatusSummary{ModuleCount: 1, Healthy: 1, ResourceCount: 2}
	line := SummaryLine(s)
	if !strings.Contains(line, "1 module") {
		t.Errorf("SummaryLine = %q, want '1 module'", line)
	}
}

// — Plural edge cases —

func TestPlural_Singular(t *testing.T) {
	if plural(1, "module", "modules") != "module" {
		t.Error("plural(1) should return singular")
	}
}

func TestPlural_Plural(t *testing.T) {
	if plural(2, "module", "modules") != "modules" {
		t.Error("plural(2) should return plural")
	}
}

func TestPlural_Zero(t *testing.T) {
	if plural(0, "module", "modules") != "modules" {
		t.Error("plural(0) should return plural")
	}
}
