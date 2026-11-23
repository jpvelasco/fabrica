package submit

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/horde"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// TestSubmitRequiresStreamAndTemplateBeforeAnyAccess verifies a missing stream
// fails before state is read or the coordinator is contacted, with or without
// --dry-run.
func TestSubmitRequiresStreamAndTemplateBeforeAnyAccess(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		var out bytes.Buffer
		client := &fakeHordeClient{}
		c := newTestCommand(&out, client, hordeProvisionedState())
		c.readState = func() (*fabricastate.State, error) {
			t.Fatal("state read before the stream/template check")
			return nil, nil
		}
		c.runtime.Config.Horde.StreamID = ""
		c.buildGraphPath = writeTempBuildGraph(t)
		c.dryRun = dryRun

		err := c.run(context.Background())
		assert.Contains(t, err.Error(), "horde.streamId/horde.templateId (or --stream/--template)")
		if client.submitCalls != 0 {
			t.Errorf("dryRun=%v: submitted without a stream", dryRun)
		}
	}
}

// TestSubmitRequestFromFlagsFileAndTarget verifies what the request carries:
// flags override config; a BuildGraph file supplies only the name and target;
// without a file, targets are sent only for --target.
func TestSubmitRequestFromFlagsFileAndTarget(t *testing.T) {
	var out bytes.Buffer
	client := &fakeHordeClient{}
	c := newTestCommand(&out, client, hordeProvisionedState())
	c.runtime.Config.Horde.Change = 5
	c.overrides = horde.JobOverrides{StreamID: "flag-stream", TemplateID: "flag-template", Change: 77, Name: "nightly"}
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := horde.CreateJobRequest{StreamID: "flag-stream", TemplateID: "flag-template", Name: "nightly", CommitID: "77", Targets: []string{"Compile"}}
	if !reflect.DeepEqual(client.lastReq, want) {
		t.Errorf("request = %+v, want %+v", client.lastReq, want)
	}

	c.buildGraphPath = ""
	c.overrides = horde.JobOverrides{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run without file: %v", err)
	}
	if client.lastReq.Targets != nil || client.lastReq.Name != "" {
		t.Errorf("no file and no --target must omit targets and name, got %+v", client.lastReq)
	}
	c.overrides.Target = "Cook"
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run with --target: %v", err)
	}
	if !reflect.DeepEqual(client.lastReq.Targets, []string{"Cook"}) {
		t.Errorf("targets = %v, want [Cook]", client.lastReq.Targets)
	}
}
