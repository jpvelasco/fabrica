package setup

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type failAtCreate struct {
	n, failAt int
	err       error
}

func (f *failAtCreate) Create(ctx context.Context, r *cloud.Resource) error {
	f.n++
	if f.n == f.failAt {
		if f.err != nil {
			return f.err
		}
		return fmt.Errorf("inject fail at create #%d type %s", f.n, r.TypeName)
	}
	r.Identifier = fmt.Sprintf("id-%d", f.n)
	return nil
}

func TestRunNoProvider(t *testing.T) {
	rt := testRuntime()
	rt.Provider = nil
	c := command{runtime: rt, out: &bytes.Buffer{}, costs: fabricacost.Global}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "no provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunIdentityError(t *testing.T) {
	rt := testRuntime()
	rt.Provider = &identityErrProvider{}
	c := command{runtime: rt, dryRun: true, out: &bytes.Buffer{}, costs: fabricacost.Global}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("err = %v", err)
	}
}

type identityErrProvider struct{ fakeProvider }

func (identityErrProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "", "", "", fmt.Errorf("no credentials")
}

func TestRunReadStateError(t *testing.T) {
	c := command{
		runtime: testRuntime(), assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return nil, fmt.Errorf("state boom") },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "state boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunBackendFlagScyllaDryRun(t *testing.T) {
	var buf bytes.Buffer
	c := command{
		runtime: testRuntime(), dryRun: true, backend: ddc.BackendScylla, out: &buf, costs: fabricacost.Global,
	}
	// scylla without ami should fail plan
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected scyllaAmiId error")
	}
	rt := testRuntime()
	rt.Config.DDC.ScyllaAmiID = "ami-scylla"
	buf.Reset()
	c = command{runtime: rt, dryRun: true, backend: ddc.BackendScylla, out: &buf, costs: fabricacost.Global}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "scylla") && !strings.Contains(out, "Scylla") {
		t.Fatalf("expected scylla in plan:\n%s", out)
	}
	if !strings.Contains(out, "single-node") && !strings.Contains(out, "not production HA") && !strings.Contains(out, "WARNING") {
		t.Fatalf("expected scylla warning:\n%s", out)
	}
}

func TestRunOpenCIDRWarning(t *testing.T) {
	var buf bytes.Buffer
	rt := testRuntime()
	rt.Config.DDC.AllowedCIDR = "0.0.0.0/0"
	c := command{runtime: rt, dryRun: true, out: &buf, costs: fabricacost.Global}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "0.0.0.0/0") {
		t.Fatalf("expected open CIDR warning:\n%s", buf.String())
	}
}

func TestRunApplyScylla(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{Account: "123", Region: "us-east-1"}
	fp := &fakeProvider{}
	rt := testRuntime()
	rt.Config.DDC.Backend = ddc.BackendScylla
	rt.Config.DDC.ScyllaAmiID = "ami-scylla"
	rt.Config.DDC.AllowedCIDR = "0.0.0.0/0"
	rt.Provider = fp
	c := command{
		runtime: rt, assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(s *fabricastate.State) error { st = s; return nil },
		createResource: fp.Create,
		writeEndpoints: func(path, content string) error { return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// role, profile, bucket, sg, ddc instance, scylla = 6
	if fp.n != 6 {
		t.Fatalf("creates = %d, want 6", fp.n)
	}
	m := st.GetModule("ddc")
	if m == nil {
		t.Fatal("missing module")
	}
	var roles []string
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSEC2Instance && r.Properties != nil {
			roles = append(roles, r.Properties["role"])
		}
	}
	if len(roles) != 2 {
		t.Fatalf("ec2 roles = %v", roles)
	}
	out := buf.String()
	if !strings.Contains(out, "0.0.0.0/0") {
		t.Fatalf("expected open CIDR on completion:\n%s", out)
	}
}

func TestRunCreateFailEachStep(t *testing.T) {
	// 5 creates for zen: role, profile, bucket, sg, instance
	for failAt := 1; failAt <= 5; failAt++ {
		t.Run(fmt.Sprintf("failAt%d", failAt), func(t *testing.T) {
			st := &fabricastate.State{}
			fc := &failAtCreate{failAt: failAt}
			c := command{
				runtime: testRuntime(), assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
				readState:      func() (*fabricastate.State, error) { return st, nil },
				writeState:     func(*fabricastate.State) error { return nil },
				createResource: fc.Create,
				writeEndpoints: func(string, string) error { return nil },
			}
			if err := c.run(context.Background()); err == nil {
				t.Fatal("expected create error")
			}
		})
	}
}

func TestRunWriteStateFailures(t *testing.T) {
	// 1st write is after IAM role (checked); 5th is after DDC instance (checked).
	for _, tc := range []struct {
		name   string
		failAt int
		msg    string
	}{
		{"after_role", 1, "disk full"},
		{"after_instance", 5, "state write after instance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fabricastate.State{}
			writes := 0
			c := command{
				runtime: testRuntime(), assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
				readState: func() (*fabricastate.State, error) { return st, nil },
				writeState: func(*fabricastate.State) error {
					writes++
					if writes == tc.failAt {
						return fmt.Errorf("%s", tc.msg)
					}
					return nil
				},
				createResource: func(ctx context.Context, r *cloud.Resource) error {
					r.Identifier = "x"
					return nil
				},
				writeEndpoints: func(string, string) error { return nil },
			}
			if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRunWriteEndpointsFail(t *testing.T) {
	st := &fabricastate.State{}
	c := command{
		runtime: testRuntime(), assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(*fabricastate.State) error { return nil },
		createResource: func(ctx context.Context, r *cloud.Resource) error { r.Identifier = "x"; return nil },
		writeEndpoints: func(string, string) error { return fmt.Errorf("perm denied") },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "perm denied") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunScyllaCreateFail(t *testing.T) {
	st := &fabricastate.State{}
	rt := testRuntime()
	rt.Config.DDC.Backend = ddc.BackendScylla
	rt.Config.DDC.ScyllaAmiID = "ami-scylla"
	// 6 creates: fail on last (scylla)
	fc := &failAtCreate{failAt: 6}
	c := command{
		runtime: rt, assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(*fabricastate.State) error { return nil },
		createResource: fc.Create,
		writeEndpoints: func(string, string) error { return nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected scylla create fail")
	}
}

func TestNewCobraExercisesWiring(t *testing.T) {
	rt := testRuntime()
	cmd := New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return globals.Options{DryRun: true} },
		&bytes.Buffer{},
	)
	if cmd.Use != "setup" {
		t.Fatal(cmd.Use)
	}
	// Execute via RunE path
	root := cmd
	root.SetArgs([]string{})
	// Need persistent dry-run from parent; call RunE through Execute after setting options via New closure
	// Re-call New with DryRun true already in optionsSource — but flags aren't set. Use run() directly already covered;
	// execute command with injected options by calling the factory again and SetArgs empty with dry run in optionsSource.
	var buf bytes.Buffer
	cmd = New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return globals.Options{DryRun: true} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("%s", buf.String())
	}
}

func TestNewRuntimeSourceError(t *testing.T) {
	cmd := New(
		func() (globals.Runtime, error) { return globals.Runtime{}, fmt.Errorf("rt fail") },
		func() globals.Options { return globals.Options{} },
		&bytes.Buffer{},
	)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected runtime error")
	}
}
