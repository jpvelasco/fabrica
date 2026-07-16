package submit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestHTTPClient(statusCode int, body string) *hordeHTTPClient {
	return &hordeHTTPClient{
		baseURL: "http://fake-horde-host:5000",
		token:   "",
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: statusCode,
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		},
	}
}

func TestSubmitJobNonSuccessIncludesBody(t *testing.T) {
	client := newTestHTTPClient(http.StatusInternalServerError, `{"error":"depot unavailable"}`)
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "test", Target: "Compile"})
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if !strings.Contains(err.Error(), "depot unavailable") {
		t.Errorf("error should contain response body; got: %v", err)
	}
}

func TestGetJobStatusNonSuccessIncludesBody(t *testing.T) {
	client := newTestHTTPClient(http.StatusBadGateway, "upstream timeout")
	_, err := client.GetJobStatus(context.Background(), "job-001")
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
	if !strings.Contains(err.Error(), "upstream timeout") {
		t.Errorf("error should contain response body; got: %v", err)
	}
}

func TestSubmitJobAuthErrorMessage(t *testing.T) {
	client := newTestHTTPClient(http.StatusUnauthorized, "")
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "test", Target: "Compile"})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("401 error should mention auth; got: %v", err)
	}
}
