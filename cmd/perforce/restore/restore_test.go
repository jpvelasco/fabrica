package restore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func readyState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	return st
}

func readyStateFn() (*fabricastate.State, error) { return readyState(), nil }

func TestRestoreRequiresForceWhenReady(t *testing.T) {
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: readyStateFn,
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "clients will disconnect") {
		t.Fatalf("expected friendlier message, got: %v", err)
	}
}

func TestRestoreDryRun(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		dryRun:    true,
		force:     true,
		backupID:  "id1",
		out:       &out,
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestRestoreConfirmReject(t *testing.T) {
	var remote int
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		confirm:   func(_, _ string) bool { return false },
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			remote++
			return cloud.RemoteResult{Stdout: meta}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Metadata is read before confirm; restore script must not run (only 1 remote call).
	if remote != 1 {
		t.Fatalf("remote calls = %d, want 1 (meta only)", remote)
	}
}

func TestRestoreSuccess(t *testing.T) {
	var out bytes.Buffer
	st := readyState()
	// Start not-ready so force not required; still set force.
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			st = s
			return nil
		},
		readCreds: func() (string, error) { return "pw", nil },
		probeTCP:  func(string) bool { return true },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{Stdout: "RESTORE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Restore complete") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestRestoreNotProvisioned(t *testing.T) {
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		backupID:  "x",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("1", "r"), nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreIncompleteMeta(t *testing.T) {
	meta := `{"id":"id1","status":"failed","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: meta}, nil
		},
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "complete") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreNoRemote(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "SSM") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreRemoteFail(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		readCreds: func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{Stderr: "boom"}, errors.New("fail")
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreCredsError(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		readCreds: func() (string, error) { return "", errors.New("missing") },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: meta}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected creds error")
	}
}

func TestRestoreNonZeroExit(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		readCreds: func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{ExitCode: 5, Stderr: "bad"}, nil
		},
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "exit 5") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreMetaReadError(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{}, errors.New("no file")
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected meta error")
	}
}

func TestRestoreUnreachableProbe(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			st = s
			return nil
		},
		readCreds: func() (string, error) { return "pw", nil },
		probeTCP:  func(string) bool { return false },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.0.1"}`)
			return nil
		},
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{Stdout: "RESTORE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Warning") {
		t.Fatalf("expected unreachable warning: %s", out.String())
	}
}

func TestRestoreWithProbeAndGetResource(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			st = s
			return nil
		},
		readCreds: func() (string, error) { return "pw", nil },
		probeTCP:  func(string) bool { return true },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.0.1"}`)
			return nil
		},
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{Stdout: "RESTORE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.GetModule("perforce").Status != "ready" {
		t.Fatalf("status = %s", st.GetModule("perforce").Status)
	}
}
