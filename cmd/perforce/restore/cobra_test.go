package restore_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/perforce/restore"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func runRestore(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(restore.New(rt, optionsSource, &out))
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"restore"}, args...)...)
}

func TestCobraRestoreDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"stopped","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: &testutil.RemoteCommandProvider{}}, nil
	}
	got, err := runRestore(t, rt, "id1", "--force", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, got)
	}
	testutil.AssertContains(t, got, "dry run")
}

func TestCobraRestoreMissingCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"stopped","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	// no credentials file
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	p := &testutil.RemoteCommandProvider{RemoteResult: cloud.RemoteResult{Stdout: meta}}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	if _, err := runRestore(t, rt, "id1", "--force", "--yes"); err == nil {
		t.Fatal("expected credentials error from New wiring")
	}
}

func TestCobraRestoreEmptyPasswordInCreds(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"stopped","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	// valid file shape, empty password → ParsePerforceAdminPassword fails (covers New closure line)
	if err := os.WriteFile(filepath.Join(".fabrica", "perforce-credentials.yaml"), []byte("admin_password: \"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	p := &testutil.RemoteCommandProvider{RemoteResult: cloud.RemoteResult{Stdout: meta}}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	if _, err := runRestore(t, rt, "id1", "--force", "--yes"); err == nil {
		t.Fatal("expected empty password parse error")
	}
}

func TestCobraRestoreRuntimeError(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("rt fail")
	}
	_, err := runRestore(t, rt, "id1", "--force")
	if err == nil {
		t.Fatal("expected runtime error")
	}
}

func TestCobraRestoreRequiresForce(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: &testutil.RemoteCommandProvider{}}, nil
	}
	_, err := runRestore(t, rt, "id1")
	if err == nil {
		t.Fatal("expected --force error when ready")
	}
	testutil.AssertContains(t, err.Error(), "--force")
}
