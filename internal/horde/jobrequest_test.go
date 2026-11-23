package horde

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreateJobRequest(t *testing.T) {
	cfg := config.HordeConfig{StreamID: "cfg-stream", TemplateID: "cfg-template", Change: 4242}
	tests := []struct {
		name string
		o    JobOverrides
		want CreateJobRequest
	}{
		{"config only", JobOverrides{}, CreateJobRequest{StreamID: "cfg-stream", TemplateID: "cfg-template", CommitID: "4242"}},
		{"overrides win", JobOverrides{StreamID: "f-stream", TemplateID: "f-template", Change: 7, Name: "n", Target: "Cook"},
			CreateJobRequest{StreamID: "f-stream", TemplateID: "f-template", Name: "n", CommitID: "7", Targets: []string{"Cook"}}},
		{"zero change keeps config", JobOverrides{Change: 0}, CreateJobRequest{StreamID: "cfg-stream", TemplateID: "cfg-template", CommitID: "4242"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewCreateJobRequest(cfg, tt.o); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	if got := NewCreateJobRequest(config.HordeConfig{StreamID: "s", TemplateID: "t"}, JobOverrides{}); got.CommitID != "" || got.Targets != nil {
		t.Errorf("unset change and target must stay empty, got %+v", got)
	}
}

func TestCreateJobRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateJobRequest
		wantErr string
	}{
		{"valid", CreateJobRequest{StreamID: "//UE5/Main", TemplateID: "build", CommitID: "99"}, ""},
		{"missing stream", CreateJobRequest{TemplateID: "t"}, "Horde jobs need a stream and template"},
		{"missing template", CreateJobRequest{StreamID: "s"}, "docs/horde-jobs.md"},
		{"stream whitespace", CreateJobRequest{StreamID: "a b", TemplateID: "t"}, "contains whitespace"},
		{"template newline", CreateJobRequest{StreamID: "s", TemplateID: "t\n"}, "contains whitespace"},
		{"negative change", CreateJobRequest{StreamID: "s", TemplateID: "t", CommitID: "-3"}, "positive Perforce changelist"},
		{"non-numeric change", CreateJobRequest{StreamID: "s", TemplateID: "t", CommitID: "abc"}, "positive Perforce changelist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestCreateJobRequestJSONGolden pins the wire shape: camelCase keys, commitId
// as a string, no arguments field, and empty optional fields omitted.
func TestCreateJobRequestJSONGolden(t *testing.T) {
	data, err := json.Marshal(CreateJobRequest{StreamID: "s", TemplateID: "t", Name: "n", CommitID: "4242", Targets: []string{"Cook"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"streamId":"s","templateId":"t","name":"n","commitId":"4242","targets":["Cook"]}`; string(data) != want {
		t.Errorf("json = %s, want %s", data, want)
	}
}
