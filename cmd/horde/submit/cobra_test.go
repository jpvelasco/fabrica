package submit_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/submit"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(submit.New(runtimeSource, optionsSource, out))
	return root
}

func runSubmit(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"submit"}, args...)...)
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	return testutil.NewTestRuntime(provider)
}

func writeTempBuildGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSubmitCobraMissingArg verifies that omitting the positional arg produces a usage error.
func TestSubmitCobraMissingArg(t *testing.T) {
	_, err := runSubmit(t, newCobraRuntime(&testutil.TestProvider{}))
	if err == nil {
		t.Fatal("expected error when buildgraph-file arg is missing")
	}
}

// TestSubmitCobraWaitFlagAccepted verifies --wait/-w is accepted (no parse error).
func TestSubmitCobraWaitFlagAccepted(t *testing.T) {
	// No state on disk → command will fail with "not provisioned", but flag parsing succeeds.
	t.Chdir(t.TempDir())
	path := writeTempBuildGraph(t)
	for _, flag := range []string{"--wait", "-w"} {
		t.Run(flag, func(t *testing.T) {
			_, err := runSubmit(t, newCobraRuntime(&testutil.TestProvider{}), flag, path)
			// Error expected (not provisioned), but not a flag-parse error.
			if err != nil && err.Error() == "unknown flag: "+flag {
				t.Fatalf("%s flag not recognised", flag)
			}
		})
	}
}

func submitProvisionedStateJSON() string {
	return testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name: "horde", Version: "", Status: "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
			},
		},
	)
}

// TestSubmitCobraDryRunNoSubmit verifies --dry-run resolves the coordinator
// and prints the preview without contacting it (regression: the flag was
// previously ignored).
func TestSubmitCobraDryRunNoSubmit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, submitProvisionedStateJSON())
	bg := writeTempBuildGraph(t)

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				ActualState: []byte(`{"PrivateIpAddress":"10.0.1.42"}`),
			},
		},
	}
	got, err := runSubmit(t, newCobraRuntime(provider), "--dry-run", bg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "Compile")
	testutil.AssertContains(t, got, "http://10.0.1.42:5000")
}

// TestSubmitCobraNotProvisioned verifies error message when no state exists.
func TestSubmitCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	path := writeTempBuildGraph(t)
	_, err := runSubmit(t, newCobraRuntime(&testutil.TestProvider{}), path)
	if err == nil {
		t.Fatal("expected error when horde not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "not provisioned")
}

// TestSubmitCobraRuntimeError verifies runtimeSource error surfaces as command error.
func TestSubmitCobraRuntimeError(t *testing.T) {
	path := writeTempBuildGraph(t)
	runtimeSource := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not found")
	}
	_, err := runSubmit(t, runtimeSource, path)
	if err == nil {
		t.Fatal("expected error from runtimeSource")
	}
}

// newCobraHTTPRuntime builds a runtime source whose Horde port matches the
// test server, so the production client path (no injected HordeClient) is
// driven end to end through ExecuteContext.
func newCobraHTTPRuntime(provider cloud.Provider, port int) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Horde.Port = port
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// startFakeHordeServer returns an httptest server standing in for the Horde
// REST API: POST /api/v1/jobs returns jobID, every status GET returns state.
func startFakeHordeServer(t *testing.T, jobID, state string) (host string, port int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"id":%q}`, jobID)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"state":%q}`, state)
	}))
	t.Cleanup(srv.Close)
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting listener address: %v", err)
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing listener port: %v", err)
	}
	return host, port
}

func cobraHTTPProvider(host string) *testutil.TestProvider {
	return &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				ActualState: []byte(fmt.Sprintf(`{"PrivateIpAddress":%q}`, host)),
			},
		},
	}
}

// TestSubmitCobraWaitCompleteExitZero verifies the command-level exit status
// for a job that completes: ExecuteContext returns nil and the progress
// lines land on stdout.
func TestSubmitCobraWaitCompleteExitZero(t *testing.T) {
	host, port := startFakeHordeServer(t, "cobra-wait-job", "complete")
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, submitProvisionedStateJSON())

	got, err := runSubmit(t, newCobraHTTPRuntime(cobraHTTPProvider(host), port), "--wait", writeTempBuildGraph(t))
	if err != nil {
		t.Fatalf("expected success exit for a completed job, got: %v", err)
	}
	testutil.AssertContains(t, got, "cobra-wait-job")
	testutil.AssertContains(t, got, "complete")
}

// TestSubmitCobraWaitErrorStateExitNonZero verifies the command-level exit
// status for a job that finishes in error: ExecuteContext returns a non-nil
// error naming the job (the #539 reproduction), so automation sees a
// non-zero exit instead of success.
func TestSubmitCobraWaitErrorStateExitNonZero(t *testing.T) {
	host, port := startFakeHordeServer(t, "cobra-err-job", "error")
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, submitProvisionedStateJSON())

	got, err := runSubmit(t, newCobraHTTPRuntime(cobraHTTPProvider(host), port), "--wait", writeTempBuildGraph(t))
	if err == nil {
		t.Fatal("expected non-zero exit for a job that finished in error")
	}
	testutil.AssertContains(t, err.Error(), "cobra-err-job")
	testutil.AssertContains(t, got, "Job cobra-err-job: error")
}
