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

func TestRestoreReadStateError(t *testing.T) {
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return nil, errors.New("io") },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreNoInstance(t *testing.T) {
	st := fabricastate.NewState("1", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "stopped", nil)
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		force:     true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "instance") {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreParseMetaError(t *testing.T) {
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
			return cloud.RemoteResult{Stdout: "not-json"}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestRestoreWriteStateWarning(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	var out bytes.Buffer
	calls := 0
	c := command{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		force:      true,
		backupID:   "id1",
		out:        &out,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return errors.New("disk") },
		readCreds:  func() (string, error) { return "pw", nil },
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
		t.Fatalf("out: %s", out.String())
	}
}

func TestRestoreConfirmAcceptWithAccountFromState(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	st.Account = "999"
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = ""
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	calls := 0
	c := command{
		runtime:    globals.Runtime{Config: cfg},
		force:      true,
		backupID:   "id1",
		out:        &bytes.Buffer{},
		confirm:    func(_, phrase string) bool { return phrase == "restore perforce 999" },
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		readCreds:  func() (string, error) { return "pw", nil },
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
}

func TestRestoreEmptyAdminPasswordScriptError(t *testing.T) {
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
		readCreds: func() (string, error) { return "", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: meta}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected GenerateRestoreScript empty password error")
	}
}

func TestRestoreEmptyBackupIDMetaScript(t *testing.T) {
	// GenerateReadMetaScript fails when backup id is empty — exercise that branch.
	st := readyState()
	st.Modules[0].Status = "stopped"
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		force:     true,
		backupID:  "",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected empty backup id error from GenerateReadMetaScript")
	}
}

func TestRestoreGetResourceErrorSkipsProbe(t *testing.T) {
	st := readyState()
	st.Modules[0].Status = "stopped"
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	var out bytes.Buffer
	calls := 0
	c := command{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		force:      true,
		backupID:   "id1",
		out:        &out,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		readCreds:  func() (string, error) { return "pw", nil },
		getResource: func(context.Context, *cloud.Resource) error {
			return errors.New("get failed")
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
