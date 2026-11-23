package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestGetInstanceProfileReadsRolesAndClassifiesErrors(t *testing.T) {
	ok := &fakeIAMClient{profile: &iam.GetInstanceProfileOutput{InstanceProfile: &iamtypes.InstanceProfile{
		InstanceProfileName: awssdk.String("workload"),
		Roles:               []iamtypes.Role{{RoleName: awssdk.String("r")}},
	}}}
	if info, err := newIamTaggingTest(t, ok).GetInstanceProfile(context.Background(), "workload"); err != nil || len(info.Roles) != 1 {
		t.Fatalf("GetInstanceProfile = %+v, %v; want one role", info, err)
	}
	for code, want := range map[string]error{"NoSuchEntity": fabricac.ErrResourceNotFound, "AccessDenied": fabricac.ErrInspectionDenied} {
		fake := &fakeIAMClient{profileErr: &smithy.GenericAPIError{Code: code, Message: "boom"}}
		if _, err := newIamTaggingTest(t, fake).GetInstanceProfile(context.Background(), "workload"); !errors.Is(err, want) {
			t.Errorf("%s: error = %v, want it to wrap %v", code, err, want)
		}
	}
}
