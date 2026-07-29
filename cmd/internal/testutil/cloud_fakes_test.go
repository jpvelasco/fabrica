package testutil

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestUbuntuAMIProvider(t *testing.T) {
	if _, ok := any(&TestProvider{}).(cloud.AMIResolver); ok {
		t.Fatal("TestProvider unexpectedly implements cloud.AMIResolver")
	}

	provider := &UbuntuAMIProvider{}
	amiID, err := provider.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err != nil || amiID != "ami-fake-ubuntu" {
		t.Fatalf("default ResolveUbuntuAMI() = (%q, %v)", amiID, err)
	}

	provider.AMIID = "ami-ubuntu-42"
	amiID, err = provider.ResolveUbuntuAMI(context.Background(), "us-west-2")
	if err != nil || amiID != "ami-ubuntu-42" {
		t.Fatalf("configured ResolveUbuntuAMI() = (%q, %v)", amiID, err)
	}

	wantErr := errors.New("AMI lookup failed")
	provider.AMIErr = wantErr
	amiID, err = provider.ResolveUbuntuAMI(context.Background(), "us-west-2")
	if amiID != "" || !errors.Is(err, wantErr) {
		t.Fatalf("failed ResolveUbuntuAMI() = (%q, %v)", amiID, err)
	}
}

func TestCodeBuildProvider(t *testing.T) {
	t.Run("project lifecycle", func(t *testing.T) {
		wantErr := errors.New("codebuild unavailable")
		provider := &CodeBuildProvider{EnsureProjectErr: wantErr, DeleteProjectErr: wantErr}

		created, err := provider.EnsureProject(context.Background(), cloud.CodeBuildProjectSpec{})
		if !created || !errors.Is(err, wantErr) || provider.EnsureProjectCalls != 1 {
			t.Fatalf("EnsureProject() = (%v, %v), calls = %d", created, err, provider.EnsureProjectCalls)
		}
		if err := provider.DeleteProject(context.Background(), "project"); !errors.Is(err, wantErr) || provider.DeleteProjectCalls != 1 {
			t.Fatalf("DeleteProject() error = %v, calls = %d", err, provider.DeleteProjectCalls)
		}

		provider.ProjectAlreadyExists = true
		created, _ = provider.EnsureProject(context.Background(), cloud.CodeBuildProjectSpec{})
		if created {
			t.Fatal("EnsureProject() reported an existing project as created")
		}
	})

	t.Run("build lifecycle", func(t *testing.T) {
		wantErr := errors.New("build unavailable")
		wantInfo := cloud.BuildInfo{ID: "build-42", Status: "SUCCEEDED"}
		wantEnv := map[string]string{"HORDE_URL": "http://10.0.1.42:5000"}
		provider := &CodeBuildProvider{
			StartBuildID:   "build-42",
			StartBuildErr:  wantErr,
			BuildInfo:      wantInfo,
			BuildStatusErr: wantErr,
			BuildLogOutput: "complete",
			BuildLogErr:    wantErr,
		}

		buildID, err := provider.StartBuild(context.Background(), "project", wantEnv)
		if buildID != "build-42" || !errors.Is(err, wantErr) || provider.StartBuildCalls != 1 {
			t.Fatalf("StartBuild() = (%q, %v), calls = %d", buildID, err, provider.StartBuildCalls)
		}
		if provider.LastStartBuildProject != "project" || !reflect.DeepEqual(provider.LastStartBuildEnv, wantEnv) {
			t.Fatalf("StartBuild() captured project %q, env %v", provider.LastStartBuildProject, provider.LastStartBuildEnv)
		}
		info, err := provider.BuildStatus(context.Background(), buildID)
		if info != wantInfo || !errors.Is(err, wantErr) {
			t.Fatalf("BuildStatus() = (%+v, %v)", info, err)
		}
		log, err := provider.BuildLog(context.Background(), buildID)
		if log != "complete" || !errors.Is(err, wantErr) {
			t.Fatalf("BuildLog() = (%q, %v)", log, err)
		}

		provider.StartBuildID = ""
		buildID, _ = provider.StartBuild(context.Background(), "project", nil)
		if buildID != "build-1" {
			t.Fatalf("default StartBuild() ID = %q", buildID)
		}
	})
}

func TestGameLiftProvider(t *testing.T) {
	t.Run("fleet creation", func(t *testing.T) {
		provider := &GameLiftProvider{}
		resource := &cloud.Resource{}
		if err := provider.CreateFleetAsync(context.Background(), resource); err != nil {
			t.Fatalf("CreateFleetAsync() error = %v", err)
		}
		if resource.Identifier != "fleet-new" || provider.CreateFleetAsyncCalls != 1 {
			t.Fatalf("CreateFleetAsync() identifier = %q, calls = %d", resource.Identifier, provider.CreateFleetAsyncCalls)
		}

		provider.FleetIdentifier = "fleet-42"
		resource = &cloud.Resource{}
		if err := provider.CreateFleetAsync(context.Background(), resource); err != nil || resource.Identifier != "fleet-42" {
			t.Fatalf("configured CreateFleetAsync() identifier = %q, error = %v", resource.Identifier, err)
		}

		resource.Identifier = "fleet-existing"
		if err := provider.CreateFleetAsync(context.Background(), resource); err != nil || resource.Identifier != "fleet-existing" {
			t.Fatalf("CreateFleetAsync() replaced existing identifier with %q, error = %v", resource.Identifier, err)
		}
		if err := provider.CreateFleetAsync(context.Background(), nil); err != nil {
			t.Fatalf("CreateFleetAsync(nil) error = %v", err)
		}

		wantErr := errors.New("create failed")
		provider.CreateFleetAsyncErr = wantErr
		if err := provider.CreateFleetAsync(context.Background(), &cloud.Resource{}); !errors.Is(err, wantErr) {
			t.Fatalf("CreateFleetAsync() error = %v", err)
		}
	})

	t.Run("fleet observations", func(t *testing.T) {
		wantErr := errors.New("observe failed")
		wantEvents := []cloud.FleetEvent{{Code: "FLEET_STATE", Message: "active"}}
		provider := &GameLiftProvider{
			FleetStatusByID:   map[string]string{"fleet-42": "ACTIVATING"},
			FleetEventsResult: wantEvents,
			FleetEventsErr:    wantErr,
		}

		info, err := provider.FleetStatus(context.Background(), "fleet-default")
		if err != nil || info != (cloud.FleetInfo{FleetID: "fleet-default", Status: "ACTIVE"}) {
			t.Fatalf("default FleetStatus() = (%+v, %v)", info, err)
		}
		info, err = provider.FleetStatus(context.Background(), "fleet-42")
		if err != nil || info.Status != "ACTIVATING" || provider.FleetStatusCalls != 2 {
			t.Fatalf("configured FleetStatus() = (%+v, %v), calls = %d", info, err, provider.FleetStatusCalls)
		}

		provider.FleetStatusErr = wantErr
		if _, err := provider.FleetStatus(context.Background(), "fleet-42"); !errors.Is(err, wantErr) {
			t.Fatalf("FleetStatus() error = %v", err)
		}
		events, err := provider.FleetEvents(context.Background(), "fleet-42")
		if !reflect.DeepEqual(events, wantEvents) || !errors.Is(err, wantErr) || provider.FleetEventsCalls != 1 {
			t.Fatalf("FleetEvents() = (%v, %v), calls = %d", events, err, provider.FleetEventsCalls)
		}
	})
}
