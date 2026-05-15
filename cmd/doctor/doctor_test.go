package doctor

import (
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestStatusSymbol(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"ok", "[OK]"},
		{"warning", "[WARN]"},
		{"fail", "[FAIL]"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := statusSymbol(tt.status)
			if got != tt.want {
				t.Errorf("statusSymbol(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestFormatDiagnosticSummary(t *testing.T) {
	tests := []struct {
		name          string
		fails         int
		warns         int
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name:    "all ok",
			fails:   0,
			warns:   0,
			wantErr: false,
		},
		{
			name:    "warnings only",
			fails:   0,
			warns:   2,
			wantErr: false,
		},
		{
			name:          "one failure",
			fails:         1,
			warns:         0,
			wantErr:       true,
			wantErrSubstr: "1 diagnostic check(s) failed",
		},
		{
			name:          "one failure with one warning",
			fails:         1,
			warns:         1,
			wantErr:       true,
			wantErrSubstr: "1 diagnostic check(s) failed",
		},
		{
			name:          "two failures",
			fails:         2,
			warns:         0,
			wantErr:       true,
			wantErrSubstr: "2 diagnostic check(s) failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := formatDiagnosticSummary(tt.fails, tt.warns)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.wantErrSubstr != "" && !contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErrSubstr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckGo(t *testing.T) {
	d := checkGo()
	if d.status != "ok" {
		t.Errorf("checkGo status = %q, want ok", d.status)
	}
	if d.message == "" {
		t.Error("checkGo message is empty")
	}
}

func TestCheckVersion(t *testing.T) {
	d := checkVersion()
	if d.status != "ok" {
		t.Errorf("checkVersion status = %q, want ok", d.status)
	}
	if d.message == "" {
		t.Error("checkVersion message is empty")
	}
}

func TestCheckRegion(t *testing.T) {
	tests := []struct {
		name        string
		region      string
		wantStatus  string
		wantContain string
	}{
		{
			name:       "region set",
			region:     "us-east-1",
			wantStatus: "ok",
		},
		{
			name:       "region empty",
			region:     "",
			wantStatus: "warning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Cloud.AWS.Region = tt.region
			r := checker{runtime: globals.Runtime{Config: cfg}}
			d := r.checkRegion()
			if d.status != tt.wantStatus {
				t.Errorf("status = %q, want %q", d.status, tt.wantStatus)
			}
		})
	}
}

func TestDiagnosticStruct(t *testing.T) {
	d := diagnostic{
		name:    "test",
		status:  "ok",
		message: "all good",
	}

	if d.name != "test" {
		t.Errorf("name = %q, want test", d.name)
	}
	if d.status != "ok" {
		t.Errorf("status = %q, want ok", d.status)
	}
	if d.message != "all good" {
		t.Errorf("message = %q, want all good", d.message)
	}
}

func TestCheckBucketUsesStateBackendChecker(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "state-bucket"
	backend := &fakeStateBackendChecker{bucketExists: true}

	d := checker{
		runtime: globals.Runtime{Config: cfg},
		backend: backend,
	}.checkBucket(context.Background())

	if d.status != "ok" {
		t.Fatalf("status = %q, want ok", d.status)
	}
	if backend.bucket != "state-bucket" {
		t.Fatalf("bucket = %q, want state-bucket", backend.bucket)
	}
}

func TestCheckTableUsesStateBackendChecker(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "state-bucket"
	cfg.State.Table = "state-locks"
	backend := &fakeStateBackendChecker{tableExists: true}

	d := checker{
		runtime: globals.Runtime{Config: cfg},
		backend: backend,
	}.checkTable(context.Background())

	if d.status != "ok" {
		t.Fatalf("status = %q, want ok", d.status)
	}
	if backend.table != "state-locks" {
		t.Fatalf("table = %q, want state-locks", backend.table)
	}
}

func TestCheckBucketReportsBackendCheckerErrors(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "state-bucket"
	backend := &fakeStateBackendChecker{bucketErr: fmt.Errorf("boom")}

	d := checker{
		runtime: globals.Runtime{Config: cfg},
		backend: backend,
	}.checkBucket(context.Background())

	if d.status != "fail" {
		t.Fatalf("status = %q, want fail", d.status)
	}
	if !contains(d.message, "boom") {
		t.Fatalf("message = %q, want boom", d.message)
	}
}

type fakeStateBackendChecker struct {
	bucket       string
	table        string
	bucketExists bool
	tableExists  bool
	bucketErr    error
	tableErr     error
}

func (f *fakeStateBackendChecker) StateBucketExists(ctx context.Context, bucket string) (bool, error) {
	f.bucket = bucket
	return f.bucketExists, f.bucketErr
}

func (f *fakeStateBackendChecker) StateLockTableExists(ctx context.Context, table string) (bool, error) {
	f.table = table
	return f.tableExists, f.tableErr
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestPrintDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		checks []diagnostic
	}{
		{
			name:   "empty",
			checks: []diagnostic{},
		},
		{
			name: "mixed",
			checks: []diagnostic{
				{"Go version", "ok", "1.25.9"},
				{"AWS credentials", "warning", "no creds"},
				{"Region", "fail", "missing"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := printDiagnostics(tt.checks)
			_ = err
		})
	}
}
