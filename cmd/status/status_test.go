package status

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func configWithBackend(bucket, table string) *config.Config {
	cfg := config.Defaults()
	cfg.State.Bucket = bucket
	cfg.State.Table = table
	return cfg
}

type fakeBackendChecker struct {
	bucket    bool
	table     bool
	bucketErr error
	tableErr  error
}

func (f fakeBackendChecker) StateBucketExists(_ context.Context, _ string) (bool, error) {
	return f.bucket, f.bucketErr
}
func (f fakeBackendChecker) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	return f.table, f.tableErr
}

func TestRunEmptyState(t *testing.T) {
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("123", "us-west-2"), nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "fabrica setup") {
		t.Errorf("empty state should suggest setup; got:\n%s", out.String())
	}
}

func TestRunReportsModules(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "p4-2024", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "perforce") || !strings.Contains(s, "ready") {
		t.Errorf("expected perforce ready line; got:\n%s", s)
	}
	if !strings.Contains(s, "2 resource") {
		t.Errorf("expected resource count; got:\n%s", s)
	}
}

func TestRunNextStepsForProvisioning(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("horde", "ami-1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "fabrica horde status") {
		t.Errorf("expected next-step hint for provisioning module; got:\n%s", out.String())
	}
}

func TestRunJSONShape(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("horde", "ami-1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		jsonOut:   true,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var report StatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(report.Modules) != 1 || report.Modules[0].Name != "horde" {
		t.Errorf("unexpected report: %+v", report)
	}
	if report.Summary.ModuleCount != 1 || report.Summary.ResourceCount != 1 {
		t.Errorf("unexpected summary: %+v", report.Summary)
	}
}

func TestRunProbeReachable(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "v", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		probe:     true,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.0.5"}`)
			return nil
		},
		probeTCP: func(address string) bool {
			if address != "10.0.0.5:1666" {
				t.Errorf("probe address = %q, want 10.0.0.5:1666", address)
			}
			return true
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "probe:responding") {
		t.Errorf("expected probe:responding; got:\n%s", out.String())
	}
}

func TestRunProbeUnreachable(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("horde", "v", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		probe:     true,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.0.9"}`)
			return nil
		},
		probeTCP: func(address string) bool { return false },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "probe:unreachable") {
		t.Errorf("expected probe:unreachable; got:\n%s", out.String())
	}
}

func TestRunProbeOffByDefault(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "v", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	out := &bytes.Buffer{}
	probed := false
	c := command{
		out:       out,
		probe:     false,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.0.5"}`)
			return nil
		},
		probeTCP: func(address string) bool { probed = true; return true },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if probed {
		t.Error("probeTCP must not be called when --probe is off")
	}
	if strings.Contains(out.String(), "probe:") {
		t.Errorf("output should not include probe info; got:\n%s", out.String())
	}
}

func TestRunBackendHealth(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	cfg := configWithBackend("my-bucket", "my-table")
	out := &bytes.Buffer{}
	c := command{
		runtime:   globals.Runtime{Config: cfg},
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		backend:   fakeBackendChecker{bucket: true, table: false},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "my-bucket [yes]") {
		t.Errorf("expected bucket yes; got:\n%s", s)
	}
	if !strings.Contains(s, "my-table [no]") {
		t.Errorf("expected table no; got:\n%s", s)
	}
}

func TestRunGetResourceFailureDegrades(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "v", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			return errBoom
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run should degrade gracefully, got: %v", err)
	}
	if !strings.Contains(out.String(), "perforce") {
		t.Errorf("expected module line despite CC failure; got:\n%s", out.String())
	}
}

func TestRunReadStateError(t *testing.T) {
	c := command{
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return nil, errBoom },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
}

var errBoom = errBoomType("boom")

type errBoomType string

func (e errBoomType) Error() string { return string(e) }
