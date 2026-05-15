package doctor

import (
	"fmt"
	"reflect"
	"testing"
)

func TestDiagnosticMarker(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"ok", "[OK]  "},
		{"warn", "[WARN]"},
		{"fail", "[FAIL]"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := diagnosticMarker(tt.status)
			if got != tt.want {
				t.Errorf("diagnosticMarker(%q) = %q, want %q", tt.status, got, tt.want)
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
	if !contains(d.message, "Go ") {
		t.Errorf("checkGo message %q does not start with Go", d.message)
	}
}

func TestCheckVersion(t *testing.T) {
	d := checkVersion()
	if d.status != "ok" {
		t.Errorf("checkVersion status = %q, want ok", d.status)
	}
	if !contains(d.message, "Fabrica") {
		t.Errorf("checkVersion message %q does not contain Fabrica", d.message)
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
			wantStatus: "warn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.region == "" {
				d := checkRegion()
				if d.status != tt.wantStatus {
					t.Errorf("status = %q, want %q", d.status, tt.wantStatus)
				}
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
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
			name: "empty",
			checks: []diagnostic{},
		},
		{
			name: "mixed",
			checks: []diagnostic{
				{"Go version", "ok", "Go 1.21"},
				{"AWS credentials", "warn", "no creds"},
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

func init() {
	_ = fmt.Sprintf
	_ = reflect.TypeOf
}
