package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

// referencedCommand builds a create command that references a pre-created profile.
func referencedCommand(out *bytes.Buffer, provider *testutil.InstanceProfileProvider) command {
	c := newTestCommand(out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.runtime.Config.Perforce.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"
	return c
}

func TestCreateReferencedProfileMakesNoIAMCreate(t *testing.T) {
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	capture := &testutil.StateWriteCapture{}
	c := referencedCommand(&bytes.Buffer{}, provider)
	c.writeState = capture.WriteFunc()
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(provider.CreatedTypes, ","); got != "AWS::EC2::SecurityGroup,AWS::EC2::Instance" {
		t.Errorf("created %s, want the security group and instance only", got)
	}
	if ds := createdDesiredState(t, &provider.InspectorProvider, cloud.TypeAWSEC2Instance); !strings.Contains(ds, `"IamInstanceProfile":"workload"`) {
		t.Errorf("instance does not attach the referenced profile: %s", ds)
	}
	if p, ok := capture.Last().GetModuleResource("perforce", cloud.TypeAWSIAMInstanceProfile); !ok || p.Properties["external"] != "true" {
		t.Errorf("profile record = %+v (found %v), want external", p, ok)
	}
}

func TestCreateReferencedProfileDryRunShowsNotManaged(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := referencedCommand(&out, provider)
	c.dryRun = true
	if err := c.run(context.Background()); err != nil || len(provider.CreatedTypes) != 0 || !strings.Contains(out.String(), "Instance Profile: workload (pre-created, not managed)") {
		t.Errorf("dry run err=%v created=%v; want no create and the pre-created row", err, provider.CreatedTypes)
	}
}

// Missing, denied, and mis-shaped profiles all fail before any create or secret write.
func TestCreateReferencedProfileFailsBeforeAnyCreate(t *testing.T) {
	tests := []struct {
		provider *testutil.InstanceProfileProvider
		wantErr  string
	}{
		{&testutil.InstanceProfileProvider{}, "workload"},
		{&testutil.InstanceProfileProvider{GetErr: cloud.ErrInspectionDenied}, "iam:GetInstanceProfile"},
		{&testutil.InstanceProfileProvider{Profiles: map[string]cloud.InstanceProfileInfo{"workload": {Roles: []string{"a", "b"}}}}, "2 roles"},
	}
	for _, tt := range tests {
		c := referencedCommand(&bytes.Buffer{}, tt.provider)
		c.writeState = testutil.StateWriteNever()
		err := c.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) || len(tt.provider.CreatedTypes) != 0 || len(tt.provider.PutParameters) != 0 {
			t.Errorf("run error = %v, created %v; want %q and no creates", err, tt.provider.CreatedTypes, tt.wantErr)
		}
	}
}
