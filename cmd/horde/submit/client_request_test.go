package submit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/horde"
)

// TestSubmitJobSendsRequestBody verifies the POST body is the typed request
// (camelCase, no arguments field) and the service-account token is sent.
func TestSubmitJobSendsRequestBody(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"job-42"}`))
	}))
	defer srv.Close()

	req := horde.CreateJobRequest{StreamID: "//UE5/Main", TemplateID: "build", CommitID: "4242", Targets: []string{"Cook"}}
	id, err := newHordeHTTPClient(srv.URL, "tok-1").SubmitJob(context.Background(), req)
	if err != nil || id != "job-42" {
		t.Fatalf("SubmitJob = %q, %v; want job-42", id, err)
	}
	want := map[string]any{"streamId": "//UE5/Main", "templateId": "build", "commitId": "4242", "targets": []any{"Cook"}}
	if !reflect.DeepEqual(gotBody, want) {
		t.Errorf("body = %v, want %v", gotBody, want)
	}
	if gotAuth != "ServiceAccount tok-1" {
		t.Errorf("Authorization = %q, want ServiceAccount tok-1", gotAuth)
	}
}

// TestSubmitJobRejectionHints verifies Horde's LogEvent message is kept and a
// stream or template rejection points at the config key to fix.
func TestSubmitJobRejectionHints(t *testing.T) {
	tests := []struct {
		name, body, wantMsg, wantHint string
		status                        int
	}{
		{"stream", `{"message":"Stream nope not found"}`, "Stream nope not found", "horde.streamId or --stream", http.StatusNotFound},
		{"template", `{"message":"Template build is not available for stream s"}`, "not available for stream", "horde.templateId or --template", http.StatusBadRequest},
		{"plain", "bad request body", "bad request body", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := routedHTTPClient(tt.status, tt.body)
			_, err := client.SubmitJob(context.Background(), horde.CreateJobRequest{StreamID: "nope", TemplateID: "build"})
			if err == nil {
				t.Fatal("expected error")
			}
			assert.Contains(t, err.Error(), tt.wantMsg)
			if tt.wantHint == "" {
				assert.NoneOf(t, err.Error(), "horde.streamId", "horde.templateId")
				return
			}
			assert.Contains(t, err.Error(), tt.wantHint)
			assert.Contains(t, err.Error(), "docs/horde-jobs.md")
		})
	}
}

// routedHTTPClient answers POST /api/v1/jobs with status/body and the job-route
// probe with 200, so a 404 is attributed to the request, not a missing API.
func routedHTTPClient(status int, body string) *hordeHTTPClient {
	return &hordeHTTPClient{
		baseURL: "http://fake-horde-host:5000",
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				code, text := http.StatusOK, `[]`
				if req.Method == http.MethodPost {
					code, text = status, body
				}
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(text))}, nil
			}),
		},
	}
}
