package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/perforce/backup"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/spf13/cobra"
)

func buildRoot(rt globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)
	root.AddCommand(backup.New(rt, func() globals.Options { return opts }, out))
	return root
}

func runBackup(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildRoot(rt, &out)
	root.SetArgs(append([]string{"backup"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

type fakeProvider struct {
	remote cloud.RemoteResult
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (f *fakeProvider) Resources() cloud.ResourceClient { return &fakeRC{} }
func (f *fakeProvider) RunCommand(context.Context, string, []string) (cloud.RemoteResult, error) {
	return f.remote, nil
}

type fakeRC struct{}

func (fakeRC) Create(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) Get(context.Context, *cloud.Resource) error    { return nil }
func (fakeRC) Update(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) Delete(context.Context, *cloud.Resource) error { return nil }
func (fakeRC) List(context.Context, string) ([]cloud.Resource, error) {
	return nil, nil
}

func writeReadyState(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	state := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-abc","properties":{}}
		]}]}`
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".fabrica", "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	if err := credentials.WriteCredentials(
		filepath.Join(".fabrica", "perforce-credentials.yaml"),
		credentials.FormatPerforce("pw"),
	); err != nil {
		t.Fatal(err)
	}
}

func TestCobraBackupDryRun(t *testing.T) {
	writeReadyState(t)
	p := &fakeProvider{}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	got, err := runBackup(t, rt, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, got)
	}
	if !bytes.Contains([]byte(got), []byte("dry run")) {
		t.Fatalf("output: %s", got)
	}
}

func TestCobraBackupList(t *testing.T) {
	writeReadyState(t)
	p := &fakeProvider{remote: cloud.RemoteResult{Stdout: ""}}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	got, err := runBackup(t, rt, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, got)
	}
	if !bytes.Contains([]byte(got), []byte("No backups")) {
		t.Fatalf("output: %s", got)
	}
}

func TestCobraBackupDeleteDryRun(t *testing.T) {
	writeReadyState(t)
	p := &fakeProvider{}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	got, err := runBackup(t, rt, "delete", "id1", "--dry-run")
	if err != nil {
		t.Fatalf("delete dry-run: %v\n%s", err, got)
	}
	if !bytes.Contains([]byte(got), []byte("Would delete")) {
		t.Fatalf("output: %s", got)
	}
}

func TestCobraBackupNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	p := &fakeProvider{}
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: p}, nil
	}
	_, err := runBackup(t, rt, "--yes")
	if err == nil {
		t.Fatal("expected not provisioned error")
	}
}
