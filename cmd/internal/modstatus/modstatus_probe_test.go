package modstatus

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDefaultProbeTCPUnreachable(t *testing.T) {
	if DefaultProbeTCP("127.0.0.1:1") {
		t.Fatal("expected unreachable closed port")
	}
}

func TestHTTPProbeUnreachable(t *testing.T) {
	if HTTPProbe("/health")("127.0.0.1:1") {
		t.Fatal("expected unreachable endpoint")
	}
}

func TestHTTPProbeRequestAndStatus(t *testing.T) {
	tests := []struct {
		name string
		code int
		want bool
	}{
		{name: "ok", code: http.StatusOK, want: true},
		{name: "not ok", code: http.StatusServiceUnavailable, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if got, want := req.URL.String(), "http://example.test:8080/health/ready"; got != want {
					t.Fatalf("URL = %q, want %q", got, want)
				}
				return &http.Response{
					StatusCode: tt.code,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			})}
			if got := httpProbeWithClient(client, "/health/ready")("example.test:8080"); got != tt.want {
				t.Fatalf("probe = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTTPProbeRequestError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("request failed")
	})}
	if httpProbeWithClient(client, "/health")("example.test") {
		t.Fatal("expected request error to report unreachable")
	}
}
