package destroy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestWrapDeleteNil(t *testing.T) {
	fn := wrapDelete(func(ctx context.Context, r *cloud.Resource) error { return nil })
	if err := fn(context.Background(), &cloud.Resource{TypeName: ddc.TypeAWSS3Bucket, Identifier: "b"}); err != nil {
		t.Fatal(err)
	}
}

func TestWrapDeleteNotFound(t *testing.T) {
	fn := wrapDelete(func(ctx context.Context, r *cloud.Resource) error {
		return cloud.ErrResourceNotFound
	})
	err := fn(context.Background(), &cloud.Resource{TypeName: ddc.TypeAWSS3Bucket, Identifier: "b"})
	if !errors.Is(err, cloud.ErrResourceNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestWrapDeleteS3NonEmpty(t *testing.T) {
	fn := wrapDelete(func(ctx context.Context, r *cloud.Resource) error {
		return fmt.Errorf("BucketNotEmpty")
	})
	err := fn(context.Background(), &cloud.Resource{TypeName: ddc.TypeAWSS3Bucket, Identifier: "my-bucket"})
	if err == nil {
		t.Fatal("expected error")
	}
	s := err.Error()
	if !strings.Contains(s, "my-bucket") || !strings.Contains(s, "not empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestWrapDeleteOtherError(t *testing.T) {
	fn := wrapDelete(func(ctx context.Context, r *cloud.Resource) error {
		return fmt.Errorf("permission denied")
	})
	err := fn(context.Background(), &cloud.Resource{TypeName: ddc.TypeAWSEC2Instance, Identifier: "i-1"})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
	// must not rewrite non-bucket errors with S3 messaging
	if strings.Contains(err.Error(), "force-delete") {
		t.Fatalf("unexpected s3 wrap: %v", err)
	}
}

func TestResourceOrderUnmarkedEC2(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: ddc.TypeAWSEC2Instance, Identifier: "i-other"},
			{TypeName: ddc.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		},
	}
	got := resourceOrder(m)
	if len(got) != 2 || got[0].Identifier != "i-ddc" {
		t.Fatalf("%+v", got)
	}
	if got[1].Identifier != "i-other" {
		t.Fatalf("other = %s", got[1].Identifier)
	}
}

func TestNewExecuteNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	cmd := New(
		func() (globals.Runtime, error) {
			return globals.Runtime{Config: &config.Config{}}, nil
		},
		func() globals.Options { return globals.Options{DryRun: true} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestNewExecuteWithModuleDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	// provision.ReadState reads from disk — write a state file via WriteState
	st := &fabricastate.State{Account: "123456789012", Region: "us-east-1"}
	st.UpsertModule("ddc", "ami", "ready", []fabricastate.ModuleResource{
		{TypeName: ddc.TypeAWSEC2Instance, Identifier: "i-1", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		{TypeName: ddc.TypeAWSS3Bucket, Identifier: "bucket"},
		{TypeName: ddc.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
	})
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	cmd := New(
		func() (globals.Runtime, error) {
			return globals.Runtime{Config: config.Defaults()}, nil
		},
		func() globals.Options { return globals.Options{DryRun: true} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "dry run") && !strings.Contains(buf.String(), "i-1") {
		t.Fatalf("%s", buf.String())
	}
}

func TestNewRuntimeError(t *testing.T) {
	cmd := New(
		func() (globals.Runtime, error) { return globals.Runtime{}, fmt.Errorf("rt err") },
		func() globals.Options { return globals.Options{} },
		io.Discard,
	)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewTeardownWiresDelete(t *testing.T) {
	fp := &delFake{}
	rt := globals.Runtime{Config: &config.Config{}, Provider: fp}
	var buf bytes.Buffer
	tc := NewTeardown(rt, &buf)
	if tc.DeleteResource == nil {
		t.Fatal("DeleteResource not wired")
	}
	if err := tc.DeleteResource(context.Background(), &cloud.Resource{
		TypeName: ddc.TypeAWSS3Bucket, Identifier: "b",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNewTeardownNilProvider(t *testing.T) {
	tc := NewTeardown(globals.Runtime{Config: &config.Config{}}, io.Discard)
	if tc.DeleteResource != nil {
		t.Fatal("expected nil DeleteResource without provider")
	}
}

func TestNewExecuteDestroyYes(t *testing.T) {
	t.Chdir(t.TempDir())
	st := &fabricastate.State{Account: "123456789012", Region: "us-east-1"}
	st.UpsertModule("ddc", "ami", "ready", []fabricastate.ModuleResource{
		{TypeName: ddc.TypeAWSEC2Instance, Identifier: "i-1", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		{TypeName: ddc.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
	})
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatal(err)
	}
	fp := &delFake{}
	var buf bytes.Buffer
	cmd := New(
		func() (globals.Runtime, error) {
			return globals.Runtime{Config: config.Defaults(), Provider: fp}, nil
		},
		func() globals.Options { return globals.Options{AssumeYes: true} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fp.deleted) == 0 {
		t.Fatal("expected deletes via New()")
	}
	if !strings.Contains(buf.String(), "destroyed") && !strings.Contains(buf.String(), "Deleted") {
		t.Fatalf("%s", buf.String())
	}
}
