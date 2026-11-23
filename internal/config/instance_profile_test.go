package config

import (
	"strings"
	"testing"
)

func TestInstanceProfileName(t *testing.T) {
	for _, tt := range []struct{ value, want, wantErr string }{
		{value: "", want: ""},
		{value: "pre-created", want: "pre-created"},
		{value: "arn:aws:iam::123456789012:instance-profile/platform/workload", want: "workload"},
		{value: "arn:aws:iam::123456789012:role/workload", wantErr: "is not an instance-profile ARN"},
		{value: "platform/workload", wantErr: "must be an instance profile name"},
	} {
		got, err := InstanceProfileName(tt.value)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("InstanceProfileName(%q) error = %v, want containing %q", tt.value, err, tt.wantErr)
			}
		} else if err != nil || got != tt.want {
			t.Errorf("InstanceProfileName(%q) = %q, %v; want %q", tt.value, got, err, tt.want)
		}
	}
}
