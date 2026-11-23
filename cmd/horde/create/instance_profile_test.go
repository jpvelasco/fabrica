package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

// A referenced coordinator profile is never created: create makes the SG and
// instance only, and fails before anything is created when the profile is missing.
func TestCreateReferencedProfileMakesNoIAMCreate(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.writeState = testutil.StateWriteNever()
	c.runtime.Config.Horde.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, typeName := range provider.CreatedTypes {
		if typeName == cloud.TypeAWSIAMRole || typeName == cloud.TypeAWSIAMInstanceProfile {
			t.Errorf("created %s for a referenced profile (created %v)", typeName, provider.CreatedTypes)
		}
	}
	if !strings.Contains(out.String(), "Instance Profile: workload (pre-created, not managed)") {
		t.Errorf("output lacks the pre-created profile row")
	}
}

func TestCreateReferencedProfileMissingFailsBeforeAnyCreate(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.runtime.Config.Horde.InstanceProfile = "workload"

	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "horde.instanceProfile") {
		t.Fatalf("run error = %v, want a horde.instanceProfile error", err)
	}
	if len(provider.CreatedTypes) != 0 {
		t.Errorf("created %v before the profile check failed", provider.CreatedTypes)
	}
}
