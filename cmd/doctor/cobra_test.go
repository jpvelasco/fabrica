package doctor_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/doctor"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// cobraFakeProvider implements cloud.Provider for black-box doctor tests.
type cobraFakeProvider struct {
	identityErr error
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient { return nil }

// buildTestRoot replicates the persistent-flag hierarchy (--json lives on root).
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(doctor.New(runtimeSource, optionsSource, out))
	return root
}

func runDoctor(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"doctor"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func okRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = "us-east-1"
	rt := globals.Runtime{Config: cfg, Provider: &cobraFakeProvider{}}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestDoctorCobra_Text(t *testing.T) {
	got, err := runDoctor(t, okRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Fabrica environment diagnostics") {
		t.Errorf("output missing header:\n%s", got)
	}
}

func TestDoctorCobra_JSON(t *testing.T) {
	got, err := runDoctor(t, okRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// JSON output must not contain the human-readable header.
	if strings.Contains(got, "Fabrica environment diagnostics") {
		t.Errorf("JSON output should not contain text header:\n%s", got)
	}
	if !strings.Contains(got, `"status"`) {
		t.Errorf("JSON output missing status field:\n%s", got)
	}
}

func TestDoctorCobra_RuntimeSourceError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("init failed")
	}
	_, err := runDoctor(t, src)
	if err == nil || !strings.Contains(err.Error(), "init failed") {
		t.Fatalf("expected init failed error, got %v", err)
	}
}
