package doctor

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/doctorchecks"
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
				} else if tt.wantErrSubstr != "" && !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err, tt.wantErrSubstr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestPrintDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		checks []doctorchecks.DoctorCheck
	}{
		{
			name:   "empty",
			checks: []doctorchecks.DoctorCheck{},
		},
		{
			name: "mixed",
			checks: []doctorchecks.DoctorCheck{
				{Name: "Go version", Status: "ok", Message: "1.25.9"},
				{Name: "AWS credentials", Status: "warning", Message: "no creds"},
				{Name: "Region", Status: "fail", Message: "missing"},
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
