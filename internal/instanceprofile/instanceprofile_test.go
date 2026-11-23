package instanceprofile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeInspector struct {
	info cloud.InstanceProfileInfo
	err  error
}

func (f fakeInspector) GetInstanceProfile(context.Context, string) (cloud.InstanceProfileInfo, error) {
	return f.info, f.err
}

func TestResolveAndExternal(t *testing.T) {
	ref, err := Resolve("arn:aws:iam::123456789012:instance-profile/platform/workload")
	if err != nil || ref.Name != "workload" || !ref.External() || len(ref.RecordedTypes()) != 1 {
		t.Errorf("Resolve(arn) = %+v, %v; want external workload recording only the profile", ref, err)
	}
	if managed, _ := Resolve(""); managed.External() || len(managed.RecordedTypes()) != 2 {
		t.Errorf("Resolve(\"\") = %+v; want a managed Ref recording role and profile", managed)
	}
	if !IsExternal(fabricastate.ModuleResource{Properties: map[string]string{PropertyExternal: "true"}}) {
		t.Error("IsExternal(marked record) = false")
	}
}

func TestVerify(t *testing.T) {
	tests := []struct {
		err     error
		roles   []string
		wantErr string
	}{
		{roles: []string{"r"}},
		{err: cloud.ErrResourceNotFound, wantErr: "no such instance profile"},
		{err: cloud.ErrInspectionDenied, wantErr: "iam:GetInstanceProfile"},
		{err: errors.New("throttled"), wantErr: "throttled"},
		{roles: []string{"a", "b"}, wantErr: "2 roles"},
	}
	for _, tt := range tests {
		err := Verify(context.Background(), fakeInspector{info: cloud.InstanceProfileInfo{Roles: tt.roles}, err: tt.err}, "perforce.instanceProfile", "workload")
		if tt.wantErr == "" && err != nil {
			t.Errorf("Verify error = %v, want nil", err)
		}
		if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "workload")) {
			t.Errorf("Verify error = %v, want it to name workload and contain %q", err, tt.wantErr)
		}
	}
}
