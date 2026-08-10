package drift

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestRun_AllInSync(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	engine := &Engine{
		State: st,
		Config: &DriftConfig{
			Account: "123456789012",
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.2xlarge","ImageId":"ami-fake"}`)
			return nil
		},
		BackendChecker: &fakeBackendChecker{bucketExists: true, tableExists: true},
	}

	report := engine.Run(context.Background())

	if report.Backend.BucketStatus != InSync {
		t.Errorf("expected bucket inSync, got %s", report.Backend.BucketStatus)
	}
	if report.Backend.TableStatus != InSync {
		t.Errorf("expected table inSync, got %s", report.Backend.TableStatus)
	}
	if report.Checked != 2 {
		t.Errorf("expected 2 checked, got %d", report.Checked)
	}
	if report.InSync != 2 {
		t.Errorf("expected 2 inSync, got %d", report.InSync)
	}
	if report.Missing != 0 {
		t.Errorf("expected 0 missing, got %d", report.Missing)
	}
	if report.Mismatch != 0 {
		t.Errorf("expected 0 mismatch, got %d", report.Mismatch)
	}
}

func TestRun_MissingResource(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return cloud.ErrResourceNotFound
		},
	}

	report := engine.Run(context.Background())

	if report.Missing != 1 {
		t.Errorf("expected 1 missing, got %d", report.Missing)
	}
	if len(report.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(report.Modules))
	}
	if report.Modules[0].Resources[0].Status != Missing {
		t.Errorf("expected missing, got %s", report.Modules[0].Resources[0].Status)
	}
}

func TestRun_AttributeMismatch(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-expected", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m5.xlarge","ImageId":"ami-different"}`)
			return nil
		},
	}

	report := engine.Run(context.Background())

	if report.Mismatch != 1 {
		t.Errorf("expected 1 mismatch, got %d", report.Mismatch)
	}
	if report.Modules[0].Resources[0].Status != Mismatch {
		t.Errorf("expected mismatch, got %s", report.Modules[0].Resources[0].Status)
	}
	if report.Modules[0].Resources[0].Details == "" {
		t.Error("expected non-empty details for mismatch")
	}
	// Both InstanceType and ImageId should be reported as mismatched.
	if !strings.Contains(report.Modules[0].Resources[0].Details, "InstanceType") {
		t.Error("expected InstanceType in mismatch details")
	}
	if !strings.Contains(report.Modules[0].Resources[0].Details, "ImageId") {
		t.Error("expected ImageId in mismatch details")
	}
}

func TestRun_InstanceTerminated(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"terminated"}}`)
			return nil
		},
	}

	report := engine.Run(context.Background())

	if report.Mismatch != 1 {
		t.Errorf("expected 1 mismatch for terminated instance, got %d", report.Mismatch)
	}
}

func TestRun_InstanceStopped(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"stopped"}}`)
			return nil
		},
	}

	report := engine.Run(context.Background())

	// Stopped is drift for modules without a stop command (Horde, Perforce,
	// Lore, DDC) — the instance should be running.
	if report.Mismatch != 1 {
		t.Errorf("expected 1 mismatch for stopped instance, got %d", report.Mismatch)
	}
	if report.Modules[0].Resources[0].Status != Mismatch {
		t.Errorf("expected mismatch for stopped instance, got %s", report.Modules[0].Resources[0].Status)
	}
}

func TestRun_BackendMissing(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")

	engine := &Engine{
		State: st,
		Config: &DriftConfig{
			Account: "123456789012",
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
		BackendChecker: &fakeBackendChecker{bucketExists: false, tableExists: false},
	}

	report := engine.Run(context.Background())

	if report.Backend.BucketStatus != Missing {
		t.Errorf("expected bucket missing, got %s", report.Backend.BucketStatus)
	}
	if report.Backend.TableStatus != Missing {
		t.Errorf("expected table missing, got %s", report.Backend.TableStatus)
	}
}

func TestRun_BackendError(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")

	engine := &Engine{
		State: st,
		Config: &DriftConfig{
			Account: "123456789012",
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
		BackendChecker: &fakeBackendChecker{bucketErr: errors.New("access denied"), tableErr: errors.New("access denied")},
	}

	report := engine.Run(context.Background())

	if report.Backend.BucketStatus != Error {
		t.Errorf("expected bucket error, got %s", report.Backend.BucketStatus)
	}
	if report.Backend.TableStatus != Error {
		t.Errorf("expected table error, got %s", report.Backend.TableStatus)
	}
}

func TestRun_NoBackendChecker(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")

	engine := &Engine{
		State: st,
		Config: &DriftConfig{
			Account: "123456789012",
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
	}

	report := engine.Run(context.Background())

	if report.Backend.BucketStatus != Error {
		t.Errorf("expected bucket error when no checker, got %s", report.Backend.BucketStatus)
	}
}

func TestRun_CodeBuildProjectMissing(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
		CodeBuildRunner: &fakeCodeBuildRunner{projectExists: false},
	}

	report := engine.Run(context.Background())

	// IAM role is in sync, CodeBuild project is missing.
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync (IAM role), got %d", report.InSync)
	}
	if report.Missing != 1 {
		t.Errorf("expected 1 missing (CodeBuild), got %d", report.Missing)
	}
}

func TestRun_CodeBuildProjectInSync(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	engine := &Engine{
		State:           st,
		CodeBuildRunner: &fakeCodeBuildRunner{projectExists: true},
	}

	report := engine.Run(context.Background())

	if report.InSync != 1 {
		t.Errorf("expected 1 inSync, got %d", report.InSync)
	}
}

func TestRun_NoCodeBuildRunner(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	engine := &Engine{
		State: st,
	}

	report := engine.Run(context.Background())

	if report.Errors != 1 {
		t.Errorf("expected 1 error (no CodeBuildRunner), got %d", report.Errors)
	}
}

func TestRun_EmptyState(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")

	engine := &Engine{
		State: st,
		Config: &DriftConfig{
			Account: "123456789012",
			Bucket:  "fabrica-state-123456789012",
			Table:   "fabrica-state-lock",
		},
	}

	report := engine.Run(context.Background())

	if report.Checked != 0 {
		t.Errorf("expected 0 checked, got %d", report.Checked)
	}
	if len(report.Modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(report.Modules))
	}
}

func TestRun_NoResourceGet(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
	}

	report := engine.Run(context.Background())

	if report.Errors != 1 {
		t.Errorf("expected 1 error (no resource get), got %d", report.Errors)
	}
}

func TestRun_ResourceGetError(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return errors.New("access denied")
		},
	}

	report := engine.Run(context.Background())

	if report.Errors != 1 {
		t.Errorf("expected 1 error, got %d", report.Errors)
	}
	if report.Modules[0].Resources[0].Details == "" {
		t.Error("expected non-empty details for error")
	}
}

func TestRun_SecurityGroupInSync(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
	}

	report := engine.Run(context.Background())

	if report.InSync != 1 {
		t.Errorf("expected 1 inSync for SG, got %d", report.InSync)
	}
}

func TestRun_IAMRoleInSync(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
	}

	report := engine.Run(context.Background())

	if report.InSync != 1 {
		t.Errorf("expected 1 inSync for IAM role, got %d", report.InSync)
	}
}

func TestRun_MultipleModules(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
	}

	report := engine.Run(context.Background())

	if len(report.Modules) != 2 {
		t.Errorf("expected 2 modules, got %d", len(report.Modules))
	}
	if report.Checked != 2 {
		t.Errorf("expected 2 checked, got %d", report.Checked)
	}
	if report.InSync != 2 {
		t.Errorf("expected 2 inSync, got %d", report.InSync)
	}
}

func TestRun_CodeBuildProjectExistsError(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	engine := &Engine{
		State:           st,
		CodeBuildRunner: &fakeCodeBuildRunner{projectErr: errors.New("access denied")},
	}

	report := engine.Run(context.Background())

	if report.Errors != 1 {
		t.Errorf("expected 1 error, got %d", report.Errors)
	}
}

func TestRun_UnknownTypeExistenceOnly(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::MadeUp::Resource", Identifier: "madeup-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
	}

	report := engine.Run(context.Background())

	// Unknown type: existence-only check with unsupported note.
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync, got %d", report.InSync)
	}
	if !strings.Contains(report.Modules[0].Resources[0].Details, "unsupported") {
		t.Errorf("expected unsupported note in details, got: %s", report.Modules[0].Resources[0].Details)
	}
}

func TestRun_AMIMismatchFromVersion(t *testing.T) {
	// AMI is stored in ModuleState.Version, not Properties — verify the
	// drift engine reads it from the correct location.
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-expected", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			// Instance type matches; AMI differs from ModuleState.Version.
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.2xlarge","ImageId":"ami-wrong"}`)
			return nil
		},
	}

	report := engine.Run(context.Background())

	if report.Mismatch != 1 {
		t.Errorf("expected 1 mismatch for AMI drift, got %d", report.Mismatch)
	}
	if !strings.Contains(report.Modules[0].Resources[0].Details, "ImageId") {
		t.Errorf("expected ImageId in mismatch details, got: %s", report.Modules[0].Resources[0].Details)
	}
}

func TestRun_PerforceImageIdInProperties(t *testing.T) {
	// Perforce records AMI in Properties["imageId"] — drift should read it
	// from there and report in-sync when it matches the live instance.
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-p4", Properties: map[string]string{
			"instanceType": "m5.xlarge",
			"imageId":      "ami-abc123",
		}},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m5.xlarge","ImageId":"ami-abc123"}`)
			return nil
		},
	}

	report := engine.Run(context.Background())
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync for Perforce with matching imageId, got InSync=%d, Mismatch=%d", report.InSync, report.Mismatch)
	}
}

func TestRun_PerforceImageIdMismatch(t *testing.T) {
	// Perforce Properties["imageId"] differs from live ImageId — should report mismatch.
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-p4", Properties: map[string]string{
			"instanceType": "m5.xlarge",
			"imageId":      "ami-expected",
		}},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m5.xlarge","ImageId":"ami-different"}`)
			return nil
		},
	}

	report := engine.Run(context.Background())
	if report.Mismatch != 1 {
		t.Errorf("expected 1 mismatch for Perforce AMI drift, got %d", report.Mismatch)
	}
	if !strings.Contains(report.Modules[0].Resources[0].Details, "ImageId") {
		t.Errorf("expected ImageId in mismatch details, got: %s", report.Modules[0].Resources[0].Details)
	}
}

func TestRun_ImageIdPropertiesFallbackToVersion(t *testing.T) {
	// When Properties["imageId"] is absent, drift falls back to ModuleState.Version.
	// This is the legacy path for Horde/Lore/DDC where AMI is stored as version.
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-legacy", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h", Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
		}},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"State":{"Name":"running"},"InstanceType":"m7i.2xlarge","ImageId":"ami-legacy"}`)
			return nil
		},
	}

	report := engine.Run(context.Background())
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync for fallback to Version, got InSync=%d, Mismatch=%d", report.InSync, report.Mismatch)
	}
}

func TestRun_ExtraResource(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
		ResourceList: func(_ context.Context, typeName string) ([]cloud.Resource, error) {
			// State has i-123; live has i-123 and i-456 (extra).
			if typeName == "AWS::EC2::Instance" {
				return []cloud.Resource{
					{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
					{TypeName: "AWS::EC2::Instance", Identifier: "i-456"},
				}, nil
			}
			return nil, nil
		},
	}

	report := engine.Run(context.Background())

	// i-123 is inSync, i-456 is Extra.
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync, got %d", report.InSync)
	}
	if report.Extra != 1 {
		t.Errorf("expected 1 extra, got %d", report.Extra)
	}
	// Find the extra resource in the report.
	foundExtra := false
	for _, md := range report.Modules {
		for _, r := range md.Resources {
			if r.Status == Extra && r.Identifier == "i-456" {
				foundExtra = true
			}
		}
	}
	if !foundExtra {
		t.Error("expected to find i-456 as Extra in drift report")
	}
}

func TestRun_ExtraResourceNoList(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-fake", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-123"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
		// No ResourceList — Extra detection should be skipped.
	}

	report := engine.Run(context.Background())

	// Only the recorded resource should appear; no Extra.
	if report.InSync != 1 {
		t.Errorf("expected 1 inSync, got %d", report.InSync)
	}
	if report.Extra != 0 {
		t.Errorf("expected 0 extra when no List, got %d", report.Extra)
	}
}

func TestRun_ExtraResourceCodeBuildSkipped(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-role"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})

	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			return nil
		},
		CodeBuildRunner: &fakeCodeBuildRunner{projectExists: true},
		ResourceList: func(_ context.Context, typeName string) ([]cloud.Resource, error) {
			// Even if IAM roles have an extra, CodeBuild should not be listed.
			if typeName == "AWS::IAM::Role" {
				return []cloud.Resource{
					{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-role"},
				}, nil
			}
			return nil, nil
		},
	}

	report := engine.Run(context.Background())

	// IAM role inSync, CodeBuild inSync, no Extra.
	if report.InSync != 2 {
		t.Errorf("expected 2 inSync, got %d", report.InSync)
	}
	if report.Extra != 0 {
		t.Errorf("expected 0 extra, got %d", report.Extra)
	}
}

// fakeBackendChecker implements cloud.StateBackendChecker for tests.
type fakeBackendChecker struct {
	bucketExists bool
	tableExists  bool
	bucketErr    error
	tableErr     error
}

func (f *fakeBackendChecker) StateBucketExists(_ context.Context, _ string) (bool, error) {
	return f.bucketExists, f.bucketErr
}

func (f *fakeBackendChecker) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	return f.tableExists, f.tableErr
}

// fakeCodeBuildRunner implements cloud.CodeBuildRunner for tests.
type fakeCodeBuildRunner struct {
	projectExists bool
	projectErr    error
}

func (f *fakeCodeBuildRunner) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return false, nil
}

func (f *fakeCodeBuildRunner) DeleteProject(_ context.Context, _ string) error {
	return nil
}

func (f *fakeCodeBuildRunner) ProjectExists(_ context.Context, _ string) (bool, error) {
	return f.projectExists, f.projectErr
}

func (f *fakeCodeBuildRunner) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "", nil
}

func (f *fakeCodeBuildRunner) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, nil
}

func (f *fakeCodeBuildRunner) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}
