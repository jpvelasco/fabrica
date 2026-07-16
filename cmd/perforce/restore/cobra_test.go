package restore_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/perforce/restore"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func runRestore(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var opts globals.Options
	var out bytes.Buffer
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(&out)
	root.SetErr(&out)
	root.AddCommand(restore.New(rt, func() globals.Options { return opts }, &out))
	root.SetArgs(append([]string{"restore"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return fakeRC{} }
func (fakeProvider) RunCommand(context.Context, string, []string) (cloud.RemoteResult, error) {
	return cloud.RemoteResult{}, nil
}

type fakeRC struct{}

func (fakeRC) Create(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) Get(context.Context, *cloud.Resource) error    { return nil }
func (fakeRC) Update(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) Delete(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) List(context.Context, string) ([]cloud.Resource, error) {
	return nil, nil
}

func TestCobraRestoreDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"stopped","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}}, nil
	}
	got, err := runRestore(t, rt, "id1", "--force", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, got)
	}
	if !bytes.Contains([]byte(got), []byte("dry run")) {
		t.Fatalf("output: %s", got)
	}
}

func TestCobraRestoreMissingCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"stopped","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc"}
		]}]}`
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	// no credentials file
	meta := `{"id":"id1","status":"complete","createdAt":"t","sizeBytes":1,"helixVersion":"2024.2","serverRoot":"/hxdepots"}`
	p := &restoreFakeProvider{remote: cloud.RemoteResult{Stdout: meta}}
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
	p := &restoreFakeProvider{remote: cloud.RemoteResult{Stdout: meta}}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	if _, err := runRestore(t, rt, "id1", "--force", "--yes"); err == nil {
		t.Fatal("expected empty password parse error")
	}
}

type restoreFakeProvider struct {
	remote cloud.RemoteResult
}

func (f *restoreFakeProvider) Name() string { return "fake" }
func (f *restoreFakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (f *restoreFakeProvider) Resources() cloud.ResourceClient { return fakeRC{} }
func (f *restoreFakeProvider) RunCommand(context.Context, string, []string) (cloud.RemoteResult, error) {
	return f.remote, nil
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
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}}, nil
	}
	_, err := runRestore(t, rt, "id1")
	if err == nil {
		t.Fatal("expected --force error when ready")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("--force")) {
		t.Fatalf("error should mention --force: %v", err)
	}
}
