package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func testRuntime() globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			DDC: config.DDCConfig{
				AmiID:    "ami-ddc",
				VPCId:    "vpc-1",
				SubnetId: "subnet-1",
			},
		},
		Provider: &testutil.InspectorProvider{},
	}
}

// wireInspector mirrors New()'s cloud.ImageInspector wiring so white-box
// tests exercise the same AMI inspection path (#513).
func wireInspector(c *command) {
	if insp, ok := c.runtime.Provider.(cloud.ImageInspector); ok {
		c.inspectImage = insp.DescribeImage
	}
}

func TestRunDryRun(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	c := command{
		runtime: testRuntime(), dryRun: true, out: &buf, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	wireInspector(&c)
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("%s", buf.String())
	}
	assert.Contains(t, buf.String(), "Root volume:      /dev/sda1 (encrypted)")
}

func TestRunLockHeldAborts(t *testing.T) {
	var buf bytes.Buffer
	c := command{
		runtime: testRuntime(), assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) {
			t.Fatal("should not read state when lock is held")
			return nil, nil
		},
		createResource: func(context.Context, *cloud.Resource) error {
			t.Fatal("should not create")
			return nil
		},
	}
	// The LockingProvider cannot inspect AMIs, so wire the seam directly;
	// inspection runs before the lock and must not mask the lock error.
	c.inspectImage = func(_ context.Context, id string) (cloud.ImageInfo, error) {
		return cloud.ImageInfo{ID: id, RootDeviceName: "/dev/sda1", RootDeviceType: "ebs"}, nil
	}
	c.runtime.Provider = &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another fabrica run holds the state lock") {
		t.Fatalf("err = %v, want held-lock abort", err)
	}
}

func TestRunAlreadyProvisioned(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	st.UpsertModule("ddc", "ami", "ready", nil)
	c := command{
		runtime: testRuntime(), assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			t.Fatal("should not create")
			return nil
		},
	}
	wireInspector(&c)
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "already provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunApplyZen(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{Account: "123456789012", Region: "us-east-1"}
	fp := &testutil.InspectorProvider{}
	rt := testRuntime()
	rt.Provider = fp
	var wrote string
	c := command{
		runtime: rt, assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(s *fabricastate.State) error { st = s; return nil },
		createResource: fp.Resources().Create,
		writeEndpoints: func(path, content string) error { wrote = content; return nil },
	}
	wireInspector(&c)
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.GetModule("ddc") == nil {
		t.Fatal("module missing")
	}
	if !strings.Contains(wrote, "backend") {
		t.Fatalf("endpoints: %s", wrote)
	}
	// role, profile, bucket, sg, instance = 5
	if fp.CreateCalls != 5 {
		t.Fatalf("creates = %d, want 5", fp.CreateCalls)
	}
}

func TestRunConfirmReject(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	c := command{
		runtime: testRuntime(), out: &buf, costs: fabricacost.Global,
		confirm:    func(string) bool { return false },
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
	}
	wireInspector(&c)
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "cancelled") && !strings.Contains(buf.String(), "Cancelled") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunMissingAmi(t *testing.T) {
	rt := testRuntime()
	rt.Config.DDC.AmiID = ""
	c := command{runtime: rt, dryRun: true, out: &bytes.Buffer{}, costs: fabricacost.Global}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// A provider that cannot inspect AMIs cannot prove the root volume would
// boot encrypted, so setup refuses before any state read or create (#513).
func TestRunRefusesWithoutImageInspector(t *testing.T) {
	rt := testRuntime()
	rt.Provider = &testutil.TestProvider{}
	c := command{
		runtime: rt, assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) {
			t.Fatal("refuse must precede state read")
			return nil, nil
		},
		createResource: func(context.Context, *cloud.Resource) error {
			t.Fatal("refuse must precede any create")
			return nil
		},
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot read the root device of ddc.amiId ami-ddc") || !strings.Contains(err.Error(), "refusing to launch") {
		t.Fatalf("err = %v, want inspector-less refuse", err)
	}
}

func TestRunInspectAMIErrorsRefuse(t *testing.T) {
	fp := &testutil.TestProvider{}
	rt := testRuntime()
	rt.Provider = fp
	c := command{
		runtime: rt, assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) {
			t.Fatal("inspect error must precede state read")
			return nil, nil
		},
	}
	c.inspectImage = func(context.Context, string) (cloud.ImageInfo, error) {
		return cloud.ImageInfo{}, errors.New("describe denied")
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "inspecting ddc.amiId ami-ddc") || !strings.Contains(err.Error(), "describe denied") {
		t.Fatalf("err = %v, want wrapped inspect error", err)
	}
	if fp.CreateCalls != 0 {
		t.Fatalf("creates = %d, want 0", fp.CreateCalls)
	}
}

// The scylla backend inspects a second AMI; both failure shapes must name
// ddc.scyllaAmiId so the operator knows which key to fix (#513).
func TestRunScyllaInspectErrorsRefuse(t *testing.T) {
	newScyllaCommand := func(t *testing.T) command {
		rt := testRuntime()
		rt.Config.DDC.ScyllaAmiID = "ami-scylla"
		t.Helper()
		return command{
			runtime: rt, backend: "scylla", assumeYes: true, out: &bytes.Buffer{}, costs: fabricacost.Global,
			readState: func() (*fabricastate.State, error) {
				t.Fatal("scylla refuse must precede state read")
				return nil, nil
			},
		}
	}
	t.Run("inspect error names the scylla AMI", func(t *testing.T) {
		c := newScyllaCommand(t)
		c.inspectImage = func(_ context.Context, id string) (cloud.ImageInfo, error) {
			if id == "ami-ddc" {
				return cloud.ImageInfo{ID: id, RootDeviceName: "/dev/sda1", RootDeviceType: "ebs"}, nil
			}
			return cloud.ImageInfo{}, errors.New("scylla describe denied")
		}
		err := c.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "inspecting ddc.scyllaAmiId ami-scylla") || !strings.Contains(err.Error(), "scylla describe denied") {
			t.Fatalf("err = %v, want scylla inspect error", err)
		}
	})
	t.Run("malformed scylla root device refuses", func(t *testing.T) {
		c := newScyllaCommand(t)
		c.inspectImage = func(_ context.Context, id string) (cloud.ImageInfo, error) {
			if id == "ami-ddc" {
				return cloud.ImageInfo{ID: id, RootDeviceName: "/dev/sda1", RootDeviceType: "ebs"}, nil
			}
			return cloud.ImageInfo{ID: id, RootDeviceName: "/dev/ram0", RootDeviceType: "ebs"}, nil
		}
		err := c.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), `ddc.scyllaAmiId ami-scylla reports root device "/dev/ram0"`) {
			t.Fatalf("err = %v, want malformed scylla root device refuse", err)
		}
	})
}
