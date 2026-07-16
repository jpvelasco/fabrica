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

func readyState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc", Properties: map[string]string{}},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
	})
	return st
}

func readyStateFn() (*fabricastate.State, error) { return readyState(), nil }

func TestCreateReadStateError(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: func() (*fabricastate.State, error) { return nil, errors.New("boom") },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateNoInstance(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", nil)
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "instance") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateCredsError(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
		readCreds: func() (string, error) { return "", errors.New("no creds") },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected creds error")
	}
}

func TestCreateWriteStateWarning(t *testing.T) {
	var out bytes.Buffer
	c := createCommand{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		out:        &out,
		now:        time.Now,
		readState:  readyStateFn,
		writeState: func(*fabricastate.State) error { return errors.New("disk full") },
		readCreds:  func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Warning") {
		t.Fatalf("expected write warning: %s", out.String())
	}
}

func TestCreateNoS3Flag(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.S3Export = true
	cfg.Perforce.Backup.S3Bucket = "b"
	var out bytes.Buffer
	c := createCommand{
		runtime:    globals.Runtime{Config: cfg},
		assumeYes:  true,
		noS3:       true,
		out:        &out,
		now:        time.Now,
		readState:  readyStateFn,
		writeState: func(*fabricastate.State) error { return nil },
		readCreds:  func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "S3 export:  disabled") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestPerforceBackupCfgNil(t *testing.T) {
	if perforceBackupCfg(nil).Path != "" {
		t.Fatal("expected empty config")
	}
}

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

func TestCreateJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		dryRun:    true,
		jsonOut:   true,
		out:       &out,
		now:       func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"dryRun": true`) && !strings.Contains(out.String(), `"dryRun":true`) {
		t.Fatalf("json dry-run: %s", out.String())
	}
}

func TestCreateS3Success(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.S3Export = true
	cfg.Perforce.Backup.S3Bucket = "bk"
	cfg.Perforce.Backup.S3Prefix = "p/"
	var out bytes.Buffer
	c := createCommand{
		runtime:    globals.Runtime{Config: cfg},
		assumeYes:  true,
		out:        &out,
		now:        time.Now,
		readState:  readyStateFn,
		writeState: func(*fabricastate.State) error { return nil },
		readCreds:  func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 0}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "s3://") && !strings.Contains(out.String(), "Backup complete") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestCreateJSONSuccess(t *testing.T) {
	var out bytes.Buffer
	st := readyState()
	c := createCommand{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		jsonOut:    true,
		out:        &out,
		now:        func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) },
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
	if !strings.Contains(out.String(), `"backupId"`) {
		t.Fatalf("json out: %s", out.String())
	}
}

func TestCreateS3ExportDryRun(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Perforce.Backup.S3Export = true
	cfg.Perforce.Backup.S3Bucket = "b"
	c := createCommand{
		runtime:   globals.Runtime{Config: cfg},
		dryRun:    true,
		out:       &out,
		now:       time.Now,
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "s3://") {
		t.Fatalf("expected s3 in dry-run: %s", out.String())
	}
}

func TestCreateS3ExportMissingBucket(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.S3Export = true
	c := createCommand{
		runtime:   globals.Runtime{Config: cfg},
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "s3Bucket") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateNoRemoteRunner(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
		// runRemote nil
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "SSM") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateRemoteFailure(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
		readCreds: func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stderr: "no agent"}, errors.New("ssm failed")
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected remote error")
	}
}

func TestCreateNonZeroExit(t *testing.T) {
	c := createCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		out:       &bytes.Buffer{},
		now:       time.Now,
		readState: readyStateFn,
		readCreds: func() (string, error) { return "pw", nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 2, Stderr: "p4 fail"}, nil
		},
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "exit 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestListTextWithEntries(t *testing.T) {
	var out bytes.Buffer
	line := `{"id":"a","status":"complete","createdAt":"t","sizeBytes":9,"helixVersion":"2024.2","serverRoot":"/hxdepots","description":"d"}`
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: line + "\n"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "a") || !strings.Contains(out.String(), "d") {
		t.Fatalf("out: %s", out.String())
	}
}

func TestListNotProvisioned(t *testing.T) {
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("1", "r"), nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestListNoRemoteAndRemoteError(t *testing.T) {
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected no remote error")
	}
	c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
		return cloud.RemoteResult{}, errors.New("ssm down")
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected remote error")
	}
}

func TestListBadJSON(t *testing.T) {
	c := listCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &bytes.Buffer{},
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: "not-json\n"}, nil
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDeleteDryRunAndConfirmReject(t *testing.T) {
	var out bytes.Buffer
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		dryRun:    true,
		backupID:  "x",
		out:       &out,
		readState: readyStateFn,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Would delete") {
		t.Fatalf("out: %s", out.String())
	}

	var remote int
	c2 := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		backupID:  "x",
		out:       &bytes.Buffer{},
		confirm:   func(string) bool { return false },
		readState: readyStateFn,
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			remote++
			return cloud.RemoteResult{}, nil
		},
	}
	if err := c2.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if remote != 0 {
		t.Fatal("should not remote on reject")
	}
}

func TestDeleteNotProvisionedAndNoRemote(t *testing.T) {
	c := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "x",
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("1", "r"), nil },
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected not provisioned")
	}
	c2 := deleteCommand{
		runtime:   globals.Runtime{Config: config.Defaults()},
		assumeYes: true,
		backupID:  "x",
		out:       &bytes.Buffer{},
		readState: readyStateFn,
	}
	if err := c2.run(context.Background()); err == nil {
		t.Fatal("expected no remote")
	}
}

func TestDeleteRemoteFail(t *testing.T) {
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
			return cloud.RemoteResult{ExitCode: 3, Stderr: "rm fail"}, nil
		},
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteWithS3URI(t *testing.T) {
	calls := 0
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots","s3Uri":"s3://b/p/id1"}`
	c := deleteCommand{
		runtime:    globals.Runtime{Config: config.Defaults()},
		assumeYes:  true,
		backupID:   "id1",
		out:        &bytes.Buffer{},
		readState:  readyStateFn,
		writeState: func(*fabricastate.State) error { return nil },
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			calls++
			if calls == 1 {
				return cloud.RemoteResult{Stdout: meta}, nil
			}
			return cloud.RemoteResult{ExitCode: 0, Stdout: "DELETE_OK"}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}
