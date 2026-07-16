package backup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestListReadStateError(t *testing.T) {
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return nil, errors.New("io") },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestListNoInstance(t *testing.T) {
	st := fabricastate.NewState("1", "r")
	st.UpsertModule("perforce", "v", "ready", nil)
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteReadStateError(t *testing.T) {
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "x",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return nil, errors.New("io") },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteNoInstance(t *testing.T) {
	st := fabricastate.NewState("1", "r")
	st.UpsertModule("perforce", "v", "ready", nil)
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "x",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDeleteEmptyBackupID(t *testing.T) {
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "",
		out:       &bytes.Buffer{},
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected empty id error")
	}
}

func TestDeleteRemoteCommandFails(t *testing.T) {
	calls := 0
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/x"}`}, nil
			}
			return cloud.RemoteResult{}, errors.New("ssm delete failed")
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected delete remote error")
	}
}

func TestDeleteWriteStateWarning(t *testing.T) {
	var out bytes.Buffer
	c := deleteCommand{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		backupID:   "id1",
		out:        &out,
		readState:  readyStateFn,
		writeState: func(*fabricastate.State) error { return errors.New("disk") },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0, Stdout: "DELETE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Warning") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestCreateGenerateScriptErrorPath(t *testing.T) {
	// Force empty password after readCreds returns empty — GenerateBackupScript fails.
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
		readCreds: func() (string, error) { return "", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected GenerateBackupScript error")
	}
}

func TestCreateNilPropertiesInit(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"}, // Properties nil
	})
	c := createCommand{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		out:        &bytes.Buffer{},
		now:        time.Now,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		readCreds:  func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range st.GetModule("perforce").Resources {
		if r.TypeName == "AWS::EC2::Instance" && r.Properties["lastBackupId"] == "" {
			t.Fatal("expected lastBackupId set on nil Properties map")
		}
	}
}
