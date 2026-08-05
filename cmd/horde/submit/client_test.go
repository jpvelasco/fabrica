package submit

import (
	"context"
	"errors"
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

// TestSubmitJob404ProbeNetworkError verifies that when the GET probe cannot
// confirm the route (transport error), the 404 is reported verbatim rather
// than mislabeled as a missing job API — we only claim a missing API when the
// probe itself returns 404.
func TestSubmitJob404ProbeNetworkError(t *testing.T) {
	var posts int
	client := &hordeHTTPClient{
		baseURL: "http://fake-horde-host:5000",
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost {
					posts++
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				return nil, errors.New("probe connection refused")
			}),
		},
	}
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "test", Target: "Compile"})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("network probe failure should keep verbatim 404; got: %v", err)
	}
	if strings.Contains(err.Error(), "no job-creation API") {
		t.Errorf("probe did not confirm the route is missing; got: %v", err)
	}
	if posts != 1 {
		t.Errorf("posts = %d, want 1", posts)
	}
}

// TestJobsRouteMissingBadURL verifies the probe treats an unbuildable request
// as "route not confirmed missing" (returns false) rather than claiming the
// job API is absent.
func TestJobsRouteMissingBadURL(t *testing.T) {
	client := &hordeHTTPClient{
		baseURL: "http://[::1]:namedport",
		http:    &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, nil })},
	}
	if client.jobsRouteMissing(context.Background()) {
		t.Error("jobsRouteMissing = true, want false for unbuildable request")
	}
}

// TestSubmitJob404ProbeSendsToken verifies the probe request carries the
// service-account token when one is configured, mirroring the POST.
func TestSubmitJob404ProbeSendsToken(t *testing.T) {
	var gotAuth string
	client := &hordeHTTPClient{
		baseURL: "http://fake-horde-host:5000",
		token:   "probe-secret",
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					gotAuth = req.Header.Get("Authorization")
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		},
	}
	_, err := client.SubmitJob(context.Background(), &buildgraph.BuildGraphJob{Name: "test", Target: "Compile"})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if gotAuth != "ServiceAccount probe-secret" {
		t.Errorf("probe Authorization = %q, want ServiceAccount probe-secret", gotAuth)
	}
}
