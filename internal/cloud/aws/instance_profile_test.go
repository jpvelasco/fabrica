package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestGetInstanceProfile(t *testing.T) {
	ok := &fakeIAMClient{profile: &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
		InstanceProfileName: awssdk.String("workload"),
		Roles:               []iamtypes.Role{{RoleName: awssdk.String("workload-role")}},
	}}}
	info, err := newIamTaggingTest(t, ok).GetInstanceProfile(context.Background(), "workload")
	if err != nil || info.Name != "workload" || len(info.Roles) != 1 || info.Roles[0] != "workload-role" {
		t.Fatalf("GetInstanceProfile = %+v, %v; want workload with role workload-role", info, err)
	}

	tests := []struct {
		name    string
		code    string
		wantIs  error
		wantMsg string
	}{
		{name: "missing profile", code: "NoSuchEntity", wantIs: fabricac.ErrResourceNotFound},
		{name: "denied read", code: "AccessDenied", wantIs: fabricac.ErrInspectionDenied, wantMsg: "iam:GetInstanceProfile"},
		{name: "other failure", code: "Throttling"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeIAMClient{profileErr: &smithy.GenericAPIError{Code: tt.code, Message: "boom"}}
			_, err := newIamTaggingTest(t, fake).GetInstanceProfile(context.Background(), "workload")
			if err == nil {
				t.Fatal("GetInstanceProfile error = nil, want an error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("error = %v, want it to wrap %v", err, tt.wantIs)
			}
			if tt.wantIs == nil && (errors.Is(err, fabricac.ErrResourceNotFound) || errors.Is(err, fabricac.ErrInspectionDenied)) {
				t.Errorf("error = %v, want it unclassified", err)
			}
			if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %v, want it to name %q", err, tt.wantMsg)
			}
		})
	}
}
