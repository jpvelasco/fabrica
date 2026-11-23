package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

// referencedCommand builds a create command whose config references a
// pre-created profile, with a provider that can read profiles.
func referencedCommand(out *bytes.Buffer, provider *testutil.InstanceProfileProvider) command {
	c := newTestCommand(out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.runtime.Config.Perforce.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"
	return c
}

func TestCreateReferencedProfileMakesNoIAMCreate(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := referencedCommand(&out, provider)
	capture := &testutil.StateWriteCapture{}
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
	profile, ok := capture.Last().GetModuleResource("perforce", cloud.TypeAWSIAMInstanceProfile)
	if !ok || profile.Identifier != "workload" || profile.Properties["external"] != "true" {
		t.Errorf("profile record = %+v (found %v), want external workload", profile, ok)
	}
	if _, ok := capture.Last().GetModuleResource("perforce", cloud.TypeAWSIAMRole); ok {
		t.Error("state records an IAM role for a referenced profile")
	}
}

func TestCreateReferencedProfileDryRunShowsNotManaged(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := referencedCommand(&out, provider)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(provider.CreatedTypes) != 0 {
		t.Errorf("dry run created %v, want nothing", provider.CreatedTypes)
	}
	if !strings.Contains(out.String(), "Instance Profile: workload (pre-created, not managed)") || strings.Contains(out.String(), "IAM Role:") {
		t.Errorf("dry-run output = %q, want the pre-created profile row and no role row", out.String())
	}
}

func TestCreateReferencedProfileFailsBeforeAnyCreate(t *testing.T) {
	tests := []struct {
		name     string
		provider *testutil.InstanceProfileProvider
		wantErr  string
	}{
		{"missing profile", &testutil.InstanceProfileProvider{}, "workload"},
		{"denied read", &testutil.InstanceProfileProvider{GetErr: cloud.ErrInspectionDenied}, "iam:GetInstanceProfile"},
		{"two roles", &testutil.InstanceProfileProvider{Profiles: map[string]cloud.InstanceProfileInfo{"workload": {Name: "workload", Roles: []string{"a", "b"}}}}, "2 roles"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			c := referencedCommand(&out, tt.provider)
			c.writeState = testutil.StateWriteNever()

			err := c.run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run error = %v, want it to contain %q", err, tt.wantErr)
			}
			if len(tt.provider.CreatedTypes) != 0 || len(tt.provider.PutParameters) != 0 {
				t.Errorf("created %v and stored %d secrets before the profile check failed", tt.provider.CreatedTypes, len(tt.provider.PutParameters))
			}
		})
	}
}
