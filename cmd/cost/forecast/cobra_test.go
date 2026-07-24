package forecast_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost/forecast"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command mirroring the production flag
// hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(forecast.New(runtimeSource, optionsSource, out))
	return root
}

func runForecast(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"forecast"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newTestRuntime returns a RuntimeSource with default config (cost is offline —
// no provider needed).
func newTestRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// perforceStateJSON seeds a provisioned perforce module so the forecast has a
// non-zero base estimate to project.
func perforceStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-1","properties":{"instanceType":"m5.xlarge","volumeSize":"500"}}
		]}]}`
}

func TestForecastCobraDefaultHorizon(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteStateFile(t, ".", perforceStateJSON())
	got, err := runForecast(t, newTestRuntime())
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	assert.Contains(t, got, "Cost forecast")
	assert.Contains(t, got, "30") // default horizon
	assert.Contains(t, got, "Confidence")
}

func TestForecastCobraCustomDays(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteStateFile(t, ".", perforceStateJSON())
	got, err := runForecast(t, newTestRuntime(), "--days", "90")
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	assert.Contains(t, got, "90")
}

func TestForecastCobraJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteStateFile(t, ".", perforceStateJSON())
	got, err := runForecast(t, newTestRuntime(), "--json", "--days", "60")
	if err != nil {
		t.Fatalf("forecast: %v", err)
	}
	var payload struct {
		Days        int     `json:"days"`
		DailyBurn   float64 `json:"dailyBurn"`
		HorizonCost float64 `json:"horizonCost"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, got)
	}
	if payload.Days != 60 || payload.DailyBurn <= 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestForecastCobraRuntimeError(t *testing.T) {
	rs := func() (globals.Runtime, error) { return globals.Runtime{}, errors.New("boom") }
	_, err := runForecast(t, rs)
	if err == nil {
		t.Fatal("expected runtime error to surface")
	}
}
