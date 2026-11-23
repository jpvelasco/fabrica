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

// A referenced coordinator profile that cannot be recorded in state stops the create.
func TestRunReferencedProfileRecordFailureStops(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.runtime.Config.Horde.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"
	c.writeState = func(s *fabricastate.State) error {
		m := s.GetModule("horde")
		if m != nil && len(m.Resources) > 0 && m.Resources[len(m.Resources)-1].TypeName == cloud.TypeAWSIAMInstanceProfile {
			return errors.New("disk full")
		}
		return nil
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recording instance profile") {
		t.Fatalf("run error = %v, want a recording instance profile error", err)
	}
}
