package config

import (
	"strings"
	"testing"
)

func TestInstanceProfileName(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{name: "empty means fabrica-managed", value: ""},
		{name: "plain name", value: "pre-created-perforce", want: "pre-created-perforce"},
		{name: "arn reduces to its name", value: "arn:aws:iam::123456789012:instance-profile/platform/workload", want: "workload"},
		{name: "role arn rejected", value: "arn:aws:iam::123456789012:role/workload", wantErr: "is not an instance-profile ARN"},
		{name: "name with slash rejected", value: "platform/workload", wantErr: "must be an instance profile name"},
		{name: "name too long rejected", value: strings.Repeat("a", 129), wantErr: "must be an instance profile name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InstanceProfileName(tt.value)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("InstanceProfileName(%q) error = %v, want containing %q", tt.value, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("InstanceProfileName(%q) = %q, %v; want %q", tt.value, got, err, tt.want)
			}
		})
	}
}
