package instanceprofile

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// fakeInspector answers GetInstanceProfile from a canned result.
type fakeInspector struct {
	info cloud.InstanceProfileInfo
	err  error
}

func (f fakeInspector) GetInstanceProfile(context.Context, string) (cloud.InstanceProfileInfo, error) {
	return f.info, f.err
}

func TestResolveRecordedTypesAndExternal(t *testing.T) {
	managed, err := Resolve("")
	if err != nil || managed.External() {
		t.Fatalf("Resolve(\"\") = %+v, %v; want a fabrica-managed Ref", managed, err)
	}
	if want := []string{cloud.TypeAWSIAMRole, cloud.TypeAWSIAMInstanceProfile}; !reflect.DeepEqual(managed.RecordedTypes(), want) {
		t.Errorf("managed RecordedTypes = %v, want %v", managed.RecordedTypes(), want)
	}

	ref, err := Resolve("arn:aws:iam::123456789012:instance-profile/platform/workload")
	if err != nil || ref.Name != "workload" || !ref.External() {
		t.Fatalf("Resolve(arn) = %+v, %v; want external workload", ref, err)
	}
	if want := []string{cloud.TypeAWSIAMInstanceProfile}; !reflect.DeepEqual(ref.RecordedTypes(), want) {
		t.Errorf("external RecordedTypes = %v, want %v (no role is created)", ref.RecordedTypes(), want)
	}
	if !IsExternal(fabricastate.ModuleResource{Properties: map[string]string{PropertyExternal: "true"}}) {
		t.Error("IsExternal(marked record) = false, want true")
	}
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		roles   []string
		wantErr []string
	}{
		{name: "one role passes", roles: []string{"r"}},
		{name: "missing names profile and key", err: cloud.ErrResourceNotFound, wantErr: []string{"workload", "perforce.instanceProfile", "no instance profile"}},
		{name: "denied names permission", err: cloud.ErrInspectionDenied, wantErr: []string{"workload", "iam:GetInstanceProfile"}},
		{name: "other failure is reported", err: errors.New("throttled"), wantErr: []string{"workload", "throttled"}},
		{name: "two roles rejected", roles: []string{"a", "b"}, wantErr: []string{"workload", "2 roles"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(context.Background(), fakeInspector{info: cloud.InstanceProfileInfo{Roles: tt.roles}, err: tt.err}, "perforce.instanceProfile", "workload")
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Verify error: %v", err)
				}
				return
			}
			for _, sub := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), sub) {
					t.Errorf("Verify error = %v, want it to contain %q", err, sub)
				}
			}
		})
	}
}
