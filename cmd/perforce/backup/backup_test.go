package backup

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func readyState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc", Properties: map[string]string{}},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
	})
	return st
}

func readyStateFn() (*fabricastate.State, error) { return readyState(), nil }

func TestCreateDryRun(t *testing.T) {
	var out bytes.Buffer
	var remoteCalls int
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		dryRun:    true,
		out:       &out,
		now:       func() time.Time { return time.Date(2026, 7, 15, 14, 30, 22, 0, time.UTC) },
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			remoteCalls++
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remoteCalls != 0 {
		t.Fatalf("dry-run remote calls = %d", remoteCalls)
	}
	if !strings.Contains(out.String(), "20260715-143022") {
		t.Fatalf("output: %s", out.String())
	}
}

func TestCreateNotProvisioned(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("1", "us-east-1"), nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateNotReady(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "ready") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateConfirmReject(t *testing.T) {
	var remoteCalls int
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		now:       time.Now,
		confirm:   func(string) bool { return false },
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			remoteCalls++
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remoteCalls != 0 {
		t.Fatal("remote should not run")
	}
}

func TestCreateSuccess(t *testing.T) {
	var out bytes.Buffer
	st := readyState()
	var wrote *fabricastate.State
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		name:      "smoke",
		out:       &out,
		now:       func() time.Time { return time.Date(2026, 7, 15, 14, 30, 22, 0, time.UTC) },
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			wrote = s
			return nil
		},
		readCreds: func() (string, error) { return "pw", nil },
		runRemote: func(_ context.Context, id string, cmds []string) (cloud.RemoteResult, error) {
			if id != "i-abc" || len(cmds) != 1 {
				t.Fatalf("unexpected remote: %s %v", id, cmds)
			}
			return cloud.RemoteResult{ExitCode: 0, Stdout: "BACKUP_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Backup complete") {
		t.Fatalf("out: %s", out.String())
	}
	m := wrote.GetModule("perforce")
	var inst fabricastate.ModuleResource
	for _, r := range m.Resources {
		if r.TypeName == "AWS::EC2::Instance" {
			inst = r
			break
		}
	}
	if inst.Properties["lastBackupId"] == "" {
		t.Fatalf("lastBackupId not set: %+v", inst.Properties)
	}
}

func TestListEmpty(t *testing.T) {
	var out bytes.Buffer
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0, Stdout: ""}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No backups") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestListJSON(t *testing.T) {
	var out bytes.Buffer
	line := `{"id":"a","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		jsonOut:   true,
		out:       &out,
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: line + "\n"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"id": "a"`) {
		t.Fatalf("out: %s", out.String())
	}
}

func TestDeleteSuccess(t *testing.T) {
	st := readyState()
	// seed last backup
	for i := range st.Modules[0].Resources {
		if st.Modules[0].Resources[i].TypeName == "AWS::EC2::Instance" {
			st.Modules[0].Resources[i].Properties = map[string]string{
				"lastBackupId": "id1",
				"lastBackupAt": "t",
			}
		}
	}
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "id1",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			st = s
			return nil
		},
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0, Stdout: "DELETE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, r := range st.GetModule("perforce").Resources {
		if r.TypeName == "AWS::EC2::Instance" && r.Properties["lastBackupId"] != "" {
			t.Fatal("lastBackupId should be cleared")
		}
	}
}
