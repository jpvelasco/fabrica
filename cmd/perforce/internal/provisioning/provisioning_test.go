package provisioning

import (
	"errors"
	"testing"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestResolve(t *testing.T) {
	readErr := errors.New("read failed")

	tests := []struct {
		name      string
		readState func() (*fabricastate.State, error)
		wantErr   string
		wantCause error
	}{
		{
			name: "read error",
			readState: func() (*fabricastate.State, error) {
				return nil, readErr
			},
			wantErr:   "reading state: read failed",
			wantCause: readErr,
		},
		{
			name: "module missing",
			readState: func() (*fabricastate.State, error) {
				return fabricastate.NewState("123", "us-west-2"), nil
			},
			wantErr: "Perforce is not provisioned. Run 'fabrica perforce create' first",
		},
		{
			name: "instance missing",
			readState: func() (*fabricastate.State, error) {
				st := fabricastate.NewState("123", "us-west-2")
				st.UpsertModule(moduleName, "", "", nil)
				return st, nil
			},
			wantErr: "Perforce instance not found in state",
		},
		{
			name: "instance identifier empty",
			readState: func() (*fabricastate.State, error) {
				st := fabricastate.NewState("123", "us-west-2")
				st.UpsertModule(moduleName, "", "", []fabricastate.ModuleResource{{TypeName: instanceType}})
				return st, nil
			},
			wantErr: "Perforce instance not found in state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.readState)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Resolve() error = %v, want %q", err, tt.wantErr)
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Fatalf("Resolve() error = %v, want wrapped error %v", err, tt.wantCause)
			}
		})
	}
}

func TestResolveReturnsCanonicalStateObjects(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule(moduleName, "1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
		{TypeName: instanceType, Identifier: "i-123"},
	})
	m := st.GetModule(moduleName)

	target, err := Resolve(func() (*fabricastate.State, error) { return st, nil })
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.State != st {
		t.Fatal("Resolve() returned a different state object")
	}
	if target.Module != m {
		t.Fatal("Resolve() returned a different module object")
	}
	if target.Instance.Identifier != "i-123" {
		t.Fatalf("instance identifier = %q, want i-123", target.Instance.Identifier)
	}
}
