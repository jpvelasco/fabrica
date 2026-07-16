package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// fakeProvider implements cloud.Provider for checkCreds tests.
type fakeProvider struct {
	identityErr error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Identity(ctx context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *fakeProvider) Resources() cloud.ResourceClient { return nil }

func TestCheckCreds(t *testing.T) {
	tests := []struct {
		name       string
		provider   cloud.Provider
		wantStatus string
		wantMsg    string
	}{
		{
			name:       "no provider",
			provider:   nil,
			wantStatus: "warning",
			wantMsg:    "no provider configured",
		},
		{
			name:       "authenticated",
			provider:   &fakeProvider{},
			wantStatus: "ok",
			wantMsg:    "authenticated",
		},
		{
			name:       "auth failure",
			provider:   &fakeProvider{identityErr: fmt.Errorf("expired token")},
			wantStatus: "fail",
			wantMsg:    "could not authenticate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checker{runtime: globals.Runtime{Provider: tt.provider}}
			d := r.checkCreds(context.Background())
			if d.status != tt.wantStatus {
				t.Errorf("status = %q, want %q", d.status, tt.wantStatus)
			}
			if !contains(d.message, tt.wantMsg) {
				t.Errorf("message = %q, want substring %q", d.message, tt.wantMsg)
			}
		})
	}
}

func TestCheckBucketWarnings(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		backend cloud.StateBackendChecker
		wantMsg string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantMsg: "not yet provisioned",
		},
		{
			name:    "empty bucket",
			cfg:     config.Defaults(),
			wantMsg: "not yet provisioned",
		},
		{
			name: "nil backend",
			cfg: func() *config.Config {
				c := config.Defaults()
				c.State.Bucket = "b"
				return c
			}(),
			backend: nil,
			wantMsg: "state backend checker unavailable",
		},
		{
			name: "bucket not found",
			cfg: func() *config.Config {
				c := config.Defaults()
				c.State.Bucket = "b"
				return c
			}(),
			backend: &fakeStateBackendChecker{bucketExists: false},
			wantMsg: "bucket not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := checker{runtime: globals.Runtime{Config: tt.cfg}, backend: tt.backend}.checkBucket(context.Background())
			if d.status != "warning" {
				t.Errorf("status = %q, want warning", d.status)
			}
			if !contains(d.message, tt.wantMsg) {
				t.Errorf("message = %q, want substring %q", d.message, tt.wantMsg)
			}
		})
	}
}

func TestCheckTableWarningsAndErrors(t *testing.T) {
	withBucket := func() *config.Config {
		c := config.Defaults()
		c.State.Bucket = "b"
		c.State.Table = "t"
		return c
	}

	tests := []struct {
		name       string
		cfg        *config.Config
		backend    cloud.StateBackendChecker
		wantStatus string
		wantMsg    string
	}{
		{name: "nil config", cfg: nil, wantStatus: "warning", wantMsg: "not yet provisioned"},
		{name: "empty bucket", cfg: config.Defaults(), wantStatus: "warning", wantMsg: "not yet provisioned"},
		{name: "nil backend", cfg: withBucket(), backend: nil, wantStatus: "warning", wantMsg: "state backend checker unavailable"},
		{name: "table not found", cfg: withBucket(), backend: &fakeStateBackendChecker{tableExists: false}, wantStatus: "warning", wantMsg: "table not found"},
		{name: "table check error", cfg: withBucket(), backend: &fakeStateBackendChecker{tableErr: fmt.Errorf("boom")}, wantStatus: "fail", wantMsg: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := checker{runtime: globals.Runtime{Config: tt.cfg}, backend: tt.backend}.checkTable(context.Background())
			if d.status != tt.wantStatus {
				t.Errorf("status = %q, want %q", d.status, tt.wantStatus)
			}
			if !contains(d.message, tt.wantMsg) {
				t.Errorf("message = %q, want substring %q", d.message, tt.wantMsg)
			}
		})
	}
}

func TestStateBackendWarning(t *testing.T) {
	d := stateBackendWarning("Thing")
	if d.name != "Thing" {
		t.Errorf("name = %q, want Thing", d.name)
	}
	if d.status != "warning" {
		t.Errorf("status = %q, want warning", d.status)
	}
	if !contains(d.message, "run fabrica setup") {
		t.Errorf("message = %q, want setup hint", d.message)
	}
}

func TestCheckerRun(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = "us-east-1"
	cfg.State.Bucket = "b"
	cfg.State.Table = "t"

	checks := checker{
		runtime: globals.Runtime{Config: cfg, Provider: &fakeProvider{}},
		backend: &fakeStateBackendChecker{bucketExists: true, tableExists: true},
	}.run(context.Background())

	if len(checks) != 6 {
		t.Fatalf("got %d checks, want 6", len(checks))
	}
	for _, d := range checks {
		if d.status != "ok" {
			t.Errorf("check %q status = %q, want ok", d.name, d.status)
		}
	}
}

func TestJSONDiagnostics(t *testing.T) {
	checks := []diagnostic{
		{"Go version", "ok", "go1.25"},
		{"Region", "warning", "not set"},
	}
	out := jsonDiagnostics(checks)
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}
	if out[0]["name"] != "Go version" || out[0]["status"] != "ok" || out[0]["message"] != "go1.25" {
		t.Errorf("entry[0] = %v", out[0])
	}
	if out[1]["status"] != "warning" {
		t.Errorf("entry[1] status = %q, want warning", out[1]["status"])
	}
}

func TestCommandRunText(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = "us-east-1"
	cfg.State.Bucket = "b"
	cfg.State.Table = "t"

	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: &fakeProvider{}},
		backend: &fakeStateBackendChecker{bucketExists: true, tableExists: true},
		out:     &buf,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "Fabrica environment diagnostics") {
		t.Errorf("output missing header:\n%s", got)
	}
	if !strings.Contains(got, "All checks passed") {
		t.Errorf("output missing summary:\n%s", got)
	}
}

func TestCommandRunTextReturnsErrorOnFailure(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Defaults()
	cfg.State.Bucket = "b"

	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: &fakeProvider{identityErr: fmt.Errorf("nope")}},
		backend: &fakeStateBackendChecker{bucketErr: fmt.Errorf("boom")},
		out:     &buf,
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error from failed checks, got nil")
	}
}

func TestCommandRunJSON(t *testing.T) {
	var buf bytes.Buffer
	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = "us-east-1"
	cfg.State.Bucket = "b"
	cfg.State.Table = "t"

	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: &fakeProvider{}},
		backend: &fakeStateBackendChecker{bucketExists: true, tableExists: true},
		json:    true,
		out:     &buf,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 6 {
		t.Fatalf("got %d diagnostics, want 6", len(parsed))
	}
}
