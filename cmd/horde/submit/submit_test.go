package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeHordeClient struct {
	submitErr   error
	submitJobID string
	statusMap   map[string]string
	statusErr   error
	submitCalls int
	statusCalls int
}

func (f *fakeHordeClient) SubmitJob(_ context.Context, _ *buildgraph.BuildGraphJob) (string, error) {
	f.submitCalls++
	if f.submitErr != nil {
		return "", f.submitErr
	}
	if f.submitJobID == "" {
		return "job-fake-001", nil
	}
	return f.submitJobID, nil
}

func (f *fakeHordeClient) GetJobStatus(_ context.Context, jobID string) (string, error) {
	f.statusCalls++
	if f.statusErr != nil {
		return "", f.statusErr
	}
	if s, ok := f.statusMap[jobID]; ok {
		return s, nil
	}
	return "waiting", nil
}

func writeTempBuildGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestCommand(out *bytes.Buffer, client HordeClient, st *fabricastate.State) command {
	cfg := config.Defaults()
	c := command{
		runtime:     globals.Runtime{Config: cfg, Provider: nil},
		out:         out,
		hordeClient: client,
		sleep:       func(time.Duration) {},
		now:         time.Now,
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	return c
}

func hordeProvisionedState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	return st
}

// TestSubmitBuildGraphParseError verifies error when BuildGraph file is invalid.
func TestSubmitBuildGraphParseError(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	badXML := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(badXML, []byte("not xml <<<"), 0644); err != nil {
		t.Fatalf("writing bad XML: %v", err)
	}

	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{}, st)
	c.buildGraphPath = badXML

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
	assertContains(t, err.Error(), "parsing BuildGraph file")
}

// TestSubmitNotProvisioned verifies error when no horde module in state.
func TestSubmitNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, &fakeHordeClient{}, st)
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when horde not provisioned")
	}
	assertContains(t, err.Error(), "not provisioned")
}

// TestSubmitFireAndForget verifies successful submit without --wait.
func TestSubmitFireAndForget(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	client := &fakeHordeClient{submitJobID: "job-abc-001"}
	c := newTestCommand(&out, client, st)
	c.buildGraphPath = writeTempBuildGraph(t)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if client.submitCalls != 1 {
		t.Errorf("submitCalls = %d, want 1", client.submitCalls)
	}
	if client.statusCalls != 0 {
		t.Errorf("statusCalls = %d, want 0 (fire-and-forget)", client.statusCalls)
	}
	assertContains(t, out.String(), "job-abc-001")
}

// TestSubmitWaitPollsUntilComplete verifies --wait polls until job is complete.
func TestSubmitWaitPollsUntilComplete(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	callCount := 0
	seqClient := &sequentialFakeClient{
		submitJobID: "job-wait-001",
		statusSeq:   []string{"waiting", "running", "complete"},
		idx:         &callCount,
	}
	c := newTestCommand(&out, seqClient, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "complete")
}

// TestSubmitWaitTimeout verifies --wait surfaces timeout after 60 minutes.
func TestSubmitWaitTimeout(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{submitJobID: "job-timeout"}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true
	startTime := time.Now()
	callCount := 0
	c.now = func() time.Time {
		callCount++
		if callCount <= 1 {
			return startTime
		}
		return startTime.Add(waitTimeout + time.Second)
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "timed out")
}

// TestSubmitClientError verifies connection error is returned.
func TestSubmitClientError(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{
		submitErr: errors.New("connection refused"),
	}, st)
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on client failure")
	}
	assertContains(t, err.Error(), "connection refused")
}

// TestSubmitReadStateError verifies error is surfaced when readState fails.
func TestSubmitReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, &fakeHordeClient{}, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk failure")
	}
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
}

// sequentialFakeClient returns status values in sequence.
type sequentialFakeClient struct {
	submitJobID string
	statusSeq   []string
	idx         *int
}

func (f *sequentialFakeClient) SubmitJob(_ context.Context, _ *buildgraph.BuildGraphJob) (string, error) {
	return f.submitJobID, nil
}

func (f *sequentialFakeClient) GetJobStatus(_ context.Context, _ string) (string, error) {
	i := *f.idx
	*f.idx++
	if i >= len(f.statusSeq) {
		return f.statusSeq[len(f.statusSeq)-1], nil
	}
	return f.statusSeq[i], nil
}

// TestSubmitNoCoordinatorIP verifies error when instance has no private IP.
func TestSubmitNoCoordinatorIP(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
	})
	c := newTestCommand(&out, nil, st)
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when no instance in state")
	}
	assertContains(t, err.Error(), "no private IP")
}

// TestSubmitMissingBuildGraphFile verifies error when file does not exist.
func TestSubmitMissingBuildGraphFile(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{}, st)
	c.buildGraphPath = "/nonexistent/BuildGraph.xml"

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	assertContains(t, err.Error(), "reading BuildGraph")
}

// TestSubmitMissingRequiredFields verifies that a BuildGraph with no agents
// produces an empty job name (the command still submits, the Horde API validates).
func TestSubmitMissingRequiredFields(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	emptyXML := filepath.Join(dir, "empty.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`
	if err := os.WriteFile(emptyXML, []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}

	st := hordeProvisionedState()
	client := &fakeHordeClient{submitJobID: "job-empty"}
	c := newTestCommand(&out, client, st)
	c.buildGraphPath = emptyXML

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "job-empty")
}

// TestSubmitWaitStatusError verifies polling error is returned.
func TestSubmitWaitStatusError(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{
		submitJobID: "job-status-err",
		statusErr:   errors.New("connection reset"),
	}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when status polling fails")
	}
	assertContains(t, err.Error(), "polling job")
}

// TestSubmitWaitErrorState verifies --wait returns nil when job errors.
func TestSubmitWaitErrorState(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	c := newTestCommand(&out, &fakeHordeClient{
		submitJobID: "job-err-state",
		statusMap:   map[string]string{"job-err-state": "error"},
	}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "error")
}

// TestSubmitWithInjectedClient verifies that when a client is injected,
// no provider is needed even if instance resource exists in state.
func TestSubmitWithInjectedClient(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
	})
	client := &fakeHordeClient{submitJobID: "job-injected"}
	c := newTestCommand(&out, client, st)
	c.buildGraphPath = writeTempBuildGraph(t)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "job-injected")
	if client.submitCalls != 1 {
		t.Errorf("submitCalls = %d, want 1", client.submitCalls)
	}
}

// newSubmitCommandWithFakeRC builds a submit command with a fake provider
// that returns the given actualState JSON via Cloud Control Get.
func newSubmitCommandWithFakeRC(t *testing.T, actualStateJSON string) command {
	t.Helper()
	st := hordeProvisionedState()
	fakeRC := &fakeResourceClient{
		getFn: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(actualStateJSON)
			return nil
		},
	}
	fakeProv := &fakeSubmitProvider{rc: fakeRC}
	cfg := config.Defaults()
	c := command{
		runtime:     globals.Runtime{Config: cfg, Provider: fakeProv},
		out:         &bytes.Buffer{},
		hordeClient: nil,
		sleep:       func(time.Duration) {},
		now:         time.Now,
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.buildGraphPath = writeTempBuildGraph(t)
	return c
}

// TestSubmitResolvePrivateIP verifies the provider-resolve path when no
// client is injected: the command resolves instance IP via Cloud Control Get.
func TestSubmitResolvePrivateIP(t *testing.T) {
	c := newSubmitCommandWithFakeRC(t, `{"PrivateIpAddress":"10.0.1.42"}`)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected connection error when no real Horde is running")
	}
	assertContains(t, err.Error(), "connecting to Horde")
}

// TestSubmitResolvePrivateIPEmptyState verifies error when Get returns empty ActualState.
func TestSubmitResolvePrivateIPEmptyState(t *testing.T) {
	c := newSubmitCommandWithFakeRC(t, `{}`)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when no private IP in ActualState")
	}
	assertContains(t, err.Error(), "no private IP")
}

// TestSubmitResolvePrivateIPGetError verifies error when Cloud Control Get fails.
func TestSubmitResolvePrivateIPGetError(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	fakeRC := &fakeResourceClient{
		getFn: func(_ context.Context, r *cloud.Resource) error {
			return errors.New("resource not found")
		},
	}
	fakeProv := &fakeSubmitProvider{rc: fakeRC}
	cfg := config.Defaults()
	c := command{
		runtime:     globals.Runtime{Config: cfg, Provider: fakeProv},
		out:         &out,
		hordeClient: nil,
		sleep:       func(time.Duration) {},
		now:         time.Now,
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when Get fails")
	}
	assertContains(t, err.Error(), "querying instance")
}

// TestSubmitResolvePrivateIPNoProvider verifies error when provider is nil
// and no client is injected.
func TestSubmitResolvePrivateIPNoProvider(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	cfg := config.Defaults()
	c := command{
		runtime:     globals.Runtime{Config: cfg, Provider: nil},
		out:         &out,
		hordeClient: nil,
		sleep:       func(time.Duration) {},
		now:         time.Now,
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil and no client injected")
	}
	assertContains(t, err.Error(), "no private IP")
}

// fakeSubmitProvider implements cloud.Provider for submit tests.
type fakeSubmitProvider struct {
	rc cloud.ResourceClient
}

func (f *fakeSubmitProvider) Name() string { return "fake" }
func (f *fakeSubmitProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (f *fakeSubmitProvider) Resources() cloud.ResourceClient { return f.rc }

// fakeResourceClient implements cloud.ResourceClient for submit tests.
type fakeResourceClient struct {
	getFn func(context.Context, *cloud.Resource) error
}

func (f *fakeResourceClient) Create(_ context.Context, r *cloud.Resource) error { return nil }
func (f *fakeResourceClient) Get(ctx context.Context, r *cloud.Resource) error {
	return f.getFn(ctx, r)
}
func (f *fakeResourceClient) Update(_ context.Context, r *cloud.Resource) error { return nil }
func (f *fakeResourceClient) Delete(_ context.Context, r *cloud.Resource) error { return nil }
func (f *fakeResourceClient) List(_ context.Context, typeName string) ([]cloud.Resource, error) {
	return nil, nil
}

// --- HTTP client tests (real HTTP via httptest) ---

func TestHordeHTTPClientSubmitSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("expected /api/v1/jobs, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"srv-job-001"}`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	jobID, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{
		Name: "MyAgent", Target: "Compile",
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if jobID != "srv-job-001" {
		t.Errorf("jobID = %q, want srv-job-001", jobID)
	}
}

func TestHordeHTTPClientSubmitWithToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"tok-job"}`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "my-secret-token")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{
		Name: "A", Target: "T",
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if gotToken != "ServiceAccount my-secret-token" {
		t.Errorf("Authorization = %q, want ServiceAccount my-secret-token", gotToken)
	}
}

func TestHordeHTTPClientSubmitAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "A", Target: "T"})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	assertContains(t, err.Error(), "auth")
}

func TestHordeHTTPClientSubmitForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "A", Target: "T"})
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	assertContains(t, err.Error(), "auth")
}

func TestHordeHTTPClientSubmitNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "A", Target: "T"})
	if err == nil {
		t.Fatal("expected non-2xx error, got nil")
	}
	assertContains(t, err.Error(), "500")
}

func TestHordeHTTPClientSubmitBadResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "A", Target: "T"})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	assertContains(t, err.Error(), "parsing response")
}

func TestHordeHTTPClientGetJobStatusSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"state":"complete"}`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	state, err := client.GetJobStatus(context.Background(), "job-123")
	if err != nil {
		t.Fatalf("GetJobStatus: %v", err)
	}
	if state != "complete" {
		t.Errorf("state = %q, want complete", state)
	}
}

func TestHordeHTTPClientGetJobStatusWithToken(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"state":"running"}`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "my-token")
	_, err := client.GetJobStatus(context.Background(), "job-123")
	if err != nil {
		t.Fatalf("GetJobStatus: %v", err)
	}
	if gotToken != "ServiceAccount my-token" {
		t.Errorf("Authorization = %q, want ServiceAccount my-token", gotToken)
	}
}

func TestHordeHTTPClientGetJobStatusNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("job not found"))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.GetJobStatus(context.Background(), "job-missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	assertContains(t, err.Error(), "404")
}

func TestHordeHTTPClientGetJobStatusInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad`))
	}))
	defer srv.Close()

	client := newHordeHTTPClient(srv.URL, "")
	_, err := client.GetJobStatus(context.Background(), "job-123")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	assertContains(t, err.Error(), "parsing response")
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, sub)
}
