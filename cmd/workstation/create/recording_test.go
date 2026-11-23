package create

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// A referenced profile that cannot be recorded in state stops the create.
func TestCreateReferencedProfileRecordFailureStops(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.runtime.Config.Workstation.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"
	c.writeState = func(s *fabricastate.State) error {
		m := s.GetModule("workstation")
		if m != nil && len(m.Resources) > 0 && m.Resources[len(m.Resources)-1].TypeName == cloud.TypeAWSIAMInstanceProfile {
			return errors.New("disk full")
		}
		return nil
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "recording instance profile") {
		t.Fatalf("run error = %v, want a recording instance profile error", err)
	}
}

// A managed role or profile whose create fails stops the create at that step.
func TestCreateManagedIAMFailureStops(t *testing.T) {
	for typeName, want := range map[string]string{
		cloud.TypeAWSIAMRole:            "creating IAM role",
		cloud.TypeAWSIAMInstanceProfile: "creating instance profile",
	} {
		var out bytes.Buffer
		provider := &testutil.InspectorProvider{}
		provider.CreateErr = map[string]error{typeName: errors.New("access denied")}
		c := newTestCommand(&out, provider, testutil.NewTestState())
		c.assumeYes = true
		if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: run error = %v, want it to contain %q", typeName, err, want)
		}
	}
}
