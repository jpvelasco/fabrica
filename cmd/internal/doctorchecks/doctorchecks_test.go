package doctorchecks

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func newTestRuntime(bucket, table, region string) globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    region,
				},
			},
			State: config.State{
				Bucket: bucket,
				Table:  table,
			},
		},
	}
}

func TestRunChecks_Count(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)
	if len(checks) != 6 {
		t.Fatalf("expected 6 checks, got %d", len(checks))
	}
}

func TestRunChecks_Names(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)

	expectedNames := []string{
		"Go version",
		"Fabrica version",
		"AWS credentials",
		"Region",
		"S3 state bucket",
		"DynamoDB lock table",
	}

	for i, want := range expectedNames {
		if checks[i].Name != want {
			t.Errorf("checks[%d].Name = %q, want %q", i, checks[i].Name, want)
		}
	}
}

func TestRunChecks_NilBackend_Warnings(t *testing.T) {
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-west-2")
	checks := RunChecks(context.Background(), rt, nil)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "warning" {
				t.Errorf("S3 state bucket status = %q, want warning", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "warning" {
				t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
			}
		}
	}
}

func TestRunChecks_NoConfig(t *testing.T) {
	rt := globals.Runtime{}
	checks := RunChecks(context.Background(), rt, nil)

	if len(checks) != 6 {
		t.Fatalf("expected 6 checks, got %d", len(checks))
	}

	// Region should be warning when config is nil
	if checks[3].Status != "warning" {
		t.Errorf("region check status = %q, want warning", checks[3].Status)
	}

	// Backend checks should be warnings when config is nil
	for _, c := range checks {
		if c.Name == "S3 state bucket" && c.Status != "warning" {
			t.Errorf("S3 state bucket status = %q, want warning", c.Status)
		}
		if c.Name == "DynamoDB lock table" && c.Status != "warning" {
			t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
		}
	}
}

func TestRunChecks_NoProvider(t *testing.T) {
	rt := globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-east-1",
				},
			},
		},
	}
	checks := RunChecks(context.Background(), rt, nil)

	// Credentials should be warning when no provider
	if checks[2].Status != "warning" {
		t.Errorf("credentials check status = %q, want warning", checks[2].Status)
	}

	// Backend checks should be warnings when no bucket configured
	for _, c := range checks {
		if c.Name == "S3 state bucket" && c.Status != "warning" {
			t.Errorf("S3 state bucket status = %q, want warning", c.Status)
		}
		if c.Name == "DynamoDB lock table" && c.Status != "warning" {
			t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
		}
	}
}

func TestRunChecks_BackendExists(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketExists: true,
		tableExists:  true,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "ok" {
				t.Errorf("S3 state bucket status = %q, want ok", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "ok" {
				t.Errorf("DynamoDB lock table status = %q, want ok", c.Status)
			}
		}
	}
}

func TestRunChecks_BackendNotFound(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketExists: false,
		tableExists:  false,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "warning" {
				t.Errorf("S3 state bucket status = %q, want warning", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "warning" {
				t.Errorf("DynamoDB lock table status = %q, want warning", c.Status)
			}
		}
	}
}

func TestRunChecks_BackendError(t *testing.T) {
	fakeBackend := &fakeBackendChecker{
		bucketErr: true,
		tableErr:  true,
	}
	rt := newTestRuntime("fabrica-state-123456789012", "fabrica-state-lock", "us-east-1")
	checks := RunChecks(context.Background(), rt, fakeBackend)

	for _, c := range checks {
		switch c.Name {
		case "S3 state bucket":
			if c.Status != "fail" {
				t.Errorf("S3 state bucket status = %q, want fail", c.Status)
			}
		case "DynamoDB lock table":
			if c.Status != "fail" {
				t.Errorf("DynamoDB lock table status = %q, want fail", c.Status)
			}
		}
	}
}

func TestCheckGo(t *testing.T) {
	c := checkGo()
	if c.Name != "Go version" || c.Status != "ok" || c.Message == "" {
		t.Errorf("checkGo = %+v, want name=Go version, status=ok, non-empty message", c)
	}
}

func TestCheckVersion(t *testing.T) {
	c := checkVersion()
	if c.Name != "Fabrica version" || c.Status != "ok" {
		t.Errorf("checkVersion = %+v, want name=Fabrica version, status=ok", c)
	}
}

func TestCheckCreds_NoProvider(t *testing.T) {
	c := checkCreds(context.Background(), globals.Runtime{})
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckRegion_Empty(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{Cloud: config.Cloud{AWS: config.AWS{}}}}
	c := checkRegion(rt)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckRegion_Set(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{Cloud: config.Cloud{AWS: config.AWS{Region: "eu-west-1"}}}}
	c := checkRegion(rt)
	if c.Status != "ok" || c.Message != "eu-west-1" {
		t.Errorf("checkRegion = %+v, want status=ok, message=eu-west-1", c)
	}
}

func TestCheckBucket_NoBackend(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{State: config.State{Bucket: "my-bucket"}}}
	c := checkBucket(context.Background(), rt, nil)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

func TestCheckTable_NoBucket(t *testing.T) {
	rt := globals.Runtime{Config: &config.Config{State: config.State{Table: "my-table"}}}
	c := checkTable(context.Background(), rt, nil)
	if c.Status != "warning" {
		t.Errorf("status = %q, want warning", c.Status)
	}
}

type fakeBackendChecker struct {
	bucketExists bool
	tableExists  bool
	bucketErr    bool
	tableErr     bool
}

func (f *fakeBackendChecker) StateBucketExists(_ context.Context, _ string) (bool, error) {
	if f.bucketErr {
		return false, context.DeadlineExceeded
	}
	return f.bucketExists, nil
}

func (f *fakeBackendChecker) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	if f.tableErr {
		return false, context.DeadlineExceeded
	}
	return f.tableExists, nil
}
