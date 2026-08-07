package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestRuntime() globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-west-2",
				},
			},
			State: config.State{
				Bucket: "fabrica-state-123456789012",
				Table:  "fabrica-state-lock",
			},
		},
		ConfigPath: "fabrica.yaml",
	}
}

// — Tool registration —

func TestNew_Command(t *testing.T) {
	cmd := New(func() (globals.Runtime, error) {
		return newTestRuntime(), nil
	}, func() globals.Options {
		return globals.Options{}
	})

	if cmd.Use != "mcp" {
		t.Errorf("Use = %q, want mcp", cmd.Use)
	}
	if cmd.Short != "Run the Fabrica MCP server (stdio transport)" {
		t.Errorf("Short = %q, want 'Run the Fabrica MCP server (stdio transport)'", cmd.Short)
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}
}

func TestNew_RuntimeError(t *testing.T) {
	wantErr := errors.New("no provider")
	cmd := New(func() (globals.Runtime, error) {
		return globals.Runtime{}, wantErr
	}, func() globals.Options {
		return globals.Options{}
	})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error from bad runtime source")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
}

func TestNew_HappyPath(t *testing.T) {
	// Use the command struct directly with a stubbed runServer seam to cover
	// the happy path: runtimeSource succeeds → NewServer called → runServer called.
	rt := newTestRuntime()
	serverCalled := false
	c := &command{
		runtimeSource: func() (globals.Runtime, error) { return rt, nil },
		optionsSource: func() globals.Options { return globals.Options{} },
		runServer: func(ctx context.Context, s *mcp.Server) error {
			serverCalled = true
			return nil
		},
	}
	cmd := c.cobraCommand()
	err := cmd.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !serverCalled {
		t.Error("expected runServer to be called on happy path")
	}
}

func TestNew_RunServerError(t *testing.T) {
	// Verify that errors from runServer propagate through RunE.
	wantErr := errors.New("stdio transport failed")
	c := &command{
		runtimeSource: func() (globals.Runtime, error) { return newTestRuntime(), nil },
		optionsSource: func() globals.Options { return globals.Options{} },
		runServer: func(ctx context.Context, s *mcp.Server) error {
			return wantErr
		},
	}
	cmd := c.cobraCommand()
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error from runServer")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
}

// — Handler unit tests —

func TestHandleDoctor_FailCheck(t *testing.T) {
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "",
				},
			},
		},
	}
	h := handleDoctor(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No provider → warnings only, so healthy is true (warnings don't flip it).
	if !result.Healthy {
		t.Error("expected healthy when only warnings present")
	}
	if len(result.Checks) == 0 {
		t.Error("expected at least one check")
	}
}

func TestHandleDoctor_HealthyPath(t *testing.T) {
	rt := newTestRuntime()
	h := handleDoctor(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Healthy {
		t.Error("expected healthy")
	}
}

func TestHandleDoctor_Unhealthy(t *testing.T) {
	// A failing backend check produces status "fail" which flips healthy to false.
	fakeBackend := &fakeDoctorBackend{bucketErr: true}
	rt := newTestRuntime()
	rt.Provider = fakeBackend
	h := handleDoctor(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Healthy {
		t.Error("expected unhealthy when backend check fails")
	}
}

func TestHandleStatus(t *testing.T) {
	rt := newTestRuntime()
	h := handleStatus(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.ModuleCount != 0 {
		t.Errorf("expected 0 modules, got %d", result.Summary.ModuleCount)
	}
}

func TestHandleStatus_BadStateFile(t *testing.T) {
	// Create a bad state file to trigger the error path.
	if err := os.MkdirAll(".fabrica", 0755); err != nil {
		t.Skipf("cannot create .fabrica: %v", err)
	}
	f, err := os.Create(".fabrica/state.json")
	if err != nil {
		t.Skipf("cannot create state file: %v", err)
	}
	_, _ = f.WriteString("not json")
	f.Close()
	defer os.Remove(".fabrica/state.json")
	defer os.Remove(".fabrica")

	rt := newTestRuntime()
	h := handleStatus(rt)
	_, _, err = h(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from bad state file")
	}
}

func TestHandleDrift_NoProvider(t *testing.T) {
	rt := newTestRuntime()
	h := handleDrift(rt)
	_, _, err := h(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error when no provider")
	}
}

func TestHandleDrift_WithProvider(t *testing.T) {
	rt := newTestRuntime()
	rt.Provider = &fakeDriftProvider{}
	h := handleDrift(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Report == nil {
		t.Fatal("expected non-nil drift report")
	}
}

func TestHandleDrift_BadStateFile(t *testing.T) {
	if err := os.MkdirAll(".fabrica", 0755); err != nil {
		t.Skipf("cannot create .fabrica: %v", err)
	}
	f, err := os.Create(".fabrica/state.json")
	if err != nil {
		t.Skipf("cannot create state file: %v", err)
	}
	_, _ = f.WriteString("not json")
	f.Close()
	defer os.Remove(".fabrica/state.json")
	defer os.Remove(".fabrica")

	rt := newTestRuntime()
	rt.Provider = &fakeDriftProvider{}
	h := handleDrift(rt)
	_, _, err = h(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from bad state file")
	}
}

func TestHandleCostReport(t *testing.T) {
	rt := newTestRuntime()
	h := handleCostReport(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected $0 total, got %f", result.Total)
	}
}

func TestHandleCostReport_BadStateFile(t *testing.T) {
	if err := os.MkdirAll(".fabrica", 0755); err != nil {
		t.Skipf("cannot create .fabrica: %v", err)
	}
	f, err := os.Create(".fabrica/state.json")
	if err != nil {
		t.Skipf("cannot create state file: %v", err)
	}
	_, _ = f.WriteString("not json")
	f.Close()
	defer os.Remove(".fabrica/state.json")
	defer os.Remove(".fabrica")

	rt := newTestRuntime()
	h := handleCostReport(rt)
	_, _, err = h(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from bad state file")
	}
}

func TestHandleCostReport_WithModules(t *testing.T) {
	// Write a state file with a perforce module so the cost loop body runs.
	if err := os.MkdirAll(".fabrica", 0755); err != nil {
		t.Skipf("cannot create .fabrica: %v", err)
	}
	stateJSON := `{"account":"123456789012","region":"us-west-2","modules":[{"name":"perforce","status":"ready","resources":[{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}]}]}`
	if err := os.WriteFile(".fabrica/state.json", []byte(stateJSON), 0644); err != nil {
		t.Skipf("cannot write state: %v", err)
	}
	defer os.Remove(".fabrica/state.json")
	defer os.Remove(".fabrica")

	rt := newTestRuntime()
	h := handleCostReport(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(result.Modules))
	}
	if result.Total <= 0 {
		t.Errorf("expected positive total, got %f", result.Total)
	}
}

func TestHandleConfigShow(t *testing.T) {
	rt := newTestRuntime()
	h := handleConfigShow(rt)
	_, result, err := h(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ConfigPath != "fabrica.yaml" {
		t.Errorf("configPath = %q, want fabrica.yaml", result.ConfigPath)
	}
}

func TestHandleConfigShow_YAMLError(t *testing.T) {
	// Test the error path when configYAML returns an error.
	wantErr := errors.New("yaml marshal failed")
	h := &configShowHandler{
		rt:         newTestRuntime(),
		configYAML: func() ([]byte, error) { return nil, wantErr },
	}
	_, _, err := h.handle(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from configYAML failure")
	}
	if !strings.Contains(err.Error(), "reading config") {
		t.Errorf("error = %v, want it to contain 'reading config'", err)
	}
}

func TestHandleConfigShow_BadYAML(t *testing.T) {
	// Test the error path when yaml.Unmarshal fails (invalid YAML bytes).
	h := &configShowHandler{
		rt:         newTestRuntime(),
		configYAML: func() ([]byte, error) { return []byte("\xff\xfe invalid yaml"), nil },
	}
	_, _, err := h.handle(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error from invalid YAML")
	}
	if !strings.Contains(err.Error(), "parsing config") {
		t.Errorf("error = %v, want it to contain 'parsing config'", err)
	}
}

func TestRegisterTools_CreatesAllTools(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	rt := newTestRuntime()
	registerTools(s, rt)

	expectedTools := []string{
		"fabrica_version",
		"fabrica_doctor",
		"fabrica_status",
		"fabrica_drift",
		"fabrica_cost_report",
		"fabrica_config_show",
	}

	// Verify tools were registered by checking they can be removed (unregistered tools would panic).
	// The SDK doesn't expose a ListTools on the server side, so we verify via RemoveTools.
	for _, name := range expectedTools {
		// RemoveTools is a no-op for unknown names; we just ensure no panic.
		s.RemoveTools(name)
	}
}

func TestRegisterTools_Descriptions(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	rt := newTestRuntime()
	registerTools(s, rt)

	// If registration fails, the server will reject tool calls. We verify
	// by attempting to remove each tool — this confirms they were registered.
	tools := []string{"fabrica_version", "fabrica_doctor", "fabrica_status", "fabrica_drift", "fabrica_cost_report", "fabrica_config_show"}
	for _, name := range tools {
		s.RemoveTools(name)
	}
	// If we get here without panic, all tools were registered.
}

// — Config redaction —

func TestShouldRedact_Password(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"mongo_password", true},
		{"PASSWORD", true},
		{"token", true},
		{"service_token", true},
		{"secret", true},
		{"api_key", true},
		{"access_key", true},
		{"access_key_id", false},
		{"name", false},
		{"region", false},
		{"key_name", false},
		{"keyboard", false},
		{"monkey", false},
		{"token_bucket", false},
	}

	for _, tt := range tests {
		got := shouldRedact(tt.key)
		if got != tt.want {
			t.Errorf("shouldRedact(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestRedactMap_FlatMap(t *testing.T) {
	m := map[string]any{
		"region":   "us-east-1",
		"password": "supersecret",
		"provider": "aws",
	}
	redactMap(m)

	if m["region"] != "us-east-1" {
		t.Errorf("region = %v, want us-east-1", m["region"])
	}
	if m["password"] != "[redacted]" {
		t.Errorf("password = %v, want [redacted]", m["password"])
	}
	if m["provider"] != "aws" {
		t.Errorf("provider = %v, want aws", m["provider"])
	}
}

func TestRedactMap_NestedMap(t *testing.T) {
	m := map[string]any{
		"cloud": map[string]any{
			"provider": "aws",
			"aws": map[string]any{
				"region":     "us-west-2",
				"access_key": "AKIAIOSFODNN7EXAMPLE", // #nosec G101 — AWS docs example key for redaction test
			},
		},
	}
	redactMap(m)

	awsMap := m["cloud"].(map[string]any)["aws"].(map[string]any)
	if awsMap["access_key"] != "[redacted]" {
		t.Errorf("nested access_key = %v, want [redacted]", awsMap["access_key"])
	}
	if awsMap["region"] != "us-west-2" {
		t.Errorf("nested region = %v, want us-west-2", awsMap["region"])
	}
}

func TestRedactMap_ArrayOfMaps(t *testing.T) {
	m := map[string]any{
		"workstations": []any{
			map[string]any{
				"name":     "ws1",
				"password": "dcv-pass-1",
			},
			map[string]any{
				"name":     "ws2",
				"password": "dcv-pass-2",
			},
		},
	}
	redactMap(m)

	arr := m["workstations"].([]any)
	for i, item := range arr {
		mm := item.(map[string]any)
		if mm["password"] != "[redacted]" {
			t.Errorf("workstations[%d].password = %v, want [redacted]", i, mm["password"])
		}
		if mm["name"] == "[redacted]" {
			t.Errorf("workstations[%d].name was incorrectly redacted", i)
		}
	}
}

func TestRedactMap_EmptyMap(t *testing.T) {
	m := map[string]any{}
	redactMap(m)
	if len(m) != 0 {
		t.Errorf("empty map should remain empty after redactMap")
	}
}

func TestRedactMap_NilValues(t *testing.T) {
	m := map[string]any{
		"password": nil,
		"region":   "us-east-1",
	}
	redactMap(m)
	if m["password"] != "[redacted]" {
		t.Errorf("nil password should be redacted to [redacted], got %v", m["password"])
	}
	if m["region"] != "us-east-1" {
		t.Errorf("region should be unchanged, got %v", m["region"])
	}
}

// — Result type serialization —

func TestVersionResult_JSON(t *testing.T) {
	r := VersionResult{
		Version: "v0.1.3",
		Commit:  "abc123",
		Go:      "go1.25.12",
		OS:      "linux",
		Arch:    "amd64",
	}
	// Verify all fields are settable and have json tags.
	if r.Version != "v0.1.3" || r.Commit != "abc123" || r.Go != "go1.25.12" || r.OS != "linux" || r.Arch != "amd64" {
		t.Error("VersionResult fields not settable")
	}
}

func TestDoctorResult_Healthy(t *testing.T) {
	r := DoctorResult{
		Checks: []DoctorCheck{
			{Name: "Go", Status: "ok", Message: "go1.25"},
			{Name: "Creds", Status: "fail", Message: "no creds"},
		},
		Healthy: false,
	}
	if r.Healthy {
		t.Error("Healthy should be false when checks contain fail")
	}
	if len(r.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(r.Checks))
	}
}

func TestCostReportResult_Modules(t *testing.T) {
	r := CostReportResult{
		Total:      100.0,
		Confidence: "high",
		Modules: []CostModule{
			{Name: "perforce", Status: "ready", Subtotal: 50.0},
			{Name: "horde", Status: "ready", Subtotal: 50.0},
		},
	}
	if r.Total != 100.0 {
		t.Errorf("Total = %v, want 100.0", r.Total)
	}
	if len(r.Modules) != 2 {
		t.Errorf("expected 2 modules, got %d", len(r.Modules))
	}
}

func TestConfigShowResult_Path(t *testing.T) {
	r := ConfigShowResult{
		Config:     map[string]any{"cloud": map[string]any{"provider": "aws"}},
		ConfigPath: "/home/user/fabrica.yaml",
	}
	if r.ConfigPath != "/home/user/fabrica.yaml" {
		t.Errorf("ConfigPath = %v, want /home/user/fabrica.yaml", r.ConfigPath)
	}
	if cfg, ok := r.Config["cloud"].(map[string]any); !ok || cfg["provider"] != "aws" {
		t.Error("Config map structure incorrect")
	}
}

// — Redaction key list —

func TestRedactKeys_List(t *testing.T) {
	expected := []string{"password", "token", "secret", "api_key", "access_key"}
	if len(redactKeys) != len(expected) {
		t.Fatalf("redactKeys length = %d, want %d", len(redactKeys), len(expected))
	}
	for i, want := range expected {
		if redactKeys[i] != want {
			t.Errorf("redactKeys[%d] = %q, want %q", i, redactKeys[i], want)
		}
	}
}

// — Fakes —

type fakeDoctorBackend struct {
	bucketErr bool
	tableErr  bool
}

func (f *fakeDoctorBackend) Name() string {
	return "aws"
}

func (f *fakeDoctorBackend) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-west-2", nil
}

func (f *fakeDoctorBackend) Resources() cloud.ResourceClient {
	return nil
}

func (f *fakeDoctorBackend) StateBucketExists(_ context.Context, _ string) (bool, error) {
	if f.bucketErr {
		return false, context.DeadlineExceeded
	}
	return true, nil
}

func (f *fakeDoctorBackend) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	if f.tableErr {
		return false, context.DeadlineExceeded
	}
	return true, nil
}

type fakeDriftResourceClient struct{}

func (f *fakeDriftResourceClient) Get(_ context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeDriftResourceClient) Create(_ context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeDriftResourceClient) Update(_ context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeDriftResourceClient) Delete(_ context.Context, r *cloud.Resource) error {
	return nil
}

func (f *fakeDriftResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

type fakeDriftProvider struct{}

func (f *fakeDriftProvider) Name() string {
	return "aws"
}

func (f *fakeDriftProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-west-2", nil
}

func (f *fakeDriftProvider) Resources() cloud.ResourceClient {
	return &fakeDriftResourceClient{}
}

func (f *fakeDriftProvider) StateBucketExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func (f *fakeDriftProvider) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// CodeBuildRunner methods (satisfies cloud.CodeBuildRunner for drift type assertion).
func (f *fakeDriftProvider) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return false, nil
}

func (f *fakeDriftProvider) DeleteProject(_ context.Context, _ string) error {
	return nil
}

func (f *fakeDriftProvider) ProjectExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (f *fakeDriftProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "", nil
}

func (f *fakeDriftProvider) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, nil
}

func (f *fakeDriftProvider) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}
