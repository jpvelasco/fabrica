package setup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// referencedSetup builds a ddc setup command whose config references a
// pre-created profile and whose provider can read it.
func referencedSetup(t *testing.T, out *bytes.Buffer, provider *testutil.InstanceProfileProvider) command {
	t.Helper()
	rt := testRuntime()
	rt.Provider = provider
	rt.Config.DDC.InstanceProfile = "arn:aws:iam::123456789012:instance-profile/platform/workload"
	st := &fabricastate.State{Account: "123456789012", Region: "us-east-1"}
	c := command{
		runtime: rt, assumeYes: true, out: out, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(s *fabricastate.State) error { st = s; return nil },
		createResource: provider.Resources().Create,
		writeEndpoints: func(string, string) error { return nil },
	}
	wireInspector(&c)
	return c
}

func TestRunReferencedProfileMakesNoIAMCreate(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{Profiles: testutil.OneRoleProfile("workload")}
	if err := referencedSetup(t, &out, provider).run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, typeName := range provider.CreatedTypes {
		if typeName == cloud.TypeAWSIAMRole || typeName == cloud.TypeAWSIAMInstanceProfile {
			t.Errorf("created %s for a referenced profile (created %v)", typeName, provider.CreatedTypes)
		}
	}
	if !strings.Contains(out.String(), "Instance profile: workload (pre-created, not managed)") {
		t.Errorf("output lacks the pre-created profile line")
	}
}

func TestRunReferencedProfileMissingFailsBeforeAnyCreate(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InstanceProfileProvider{}
	err := referencedSetup(t, &out, provider).run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ddc.instanceProfile") {
		t.Fatalf("run error = %v, want a ddc.instanceProfile error", err)
	}
	if len(provider.CreatedTypes) != 0 {
		t.Errorf("created %v before the profile check failed", provider.CreatedTypes)
	}
}
