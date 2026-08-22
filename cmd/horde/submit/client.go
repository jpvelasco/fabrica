package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
)

// HordeClient abstracts communication with the Horde REST API.
// The interface lives here (not in internal/) because only submit needs it in V1.
type HordeClient interface {
	SubmitJob(ctx context.Context, job *buildgraph.BuildGraphJob) (jobID string, err error)
	GetJobStatus(ctx context.Context, jobID string) (state string, err error)
}

// hordeHTTPTimeout bounds every Horde API request. Without it a server that
// accepts TCP but never responds hangs submit/status indefinitely; command
// contexts still cancel sooner when the user interrupts.
const hordeHTTPTimeout = 30 * time.Second

type hordeHTTPClient struct {
	baseURL string // e.g. "http://10.0.1.42:5000"
	token   string // service account token (optional)
	http    *http.Client
}

func newHordeHTTPClient(baseURL, token string) *hordeHTTPClient {
	return &hordeHTTPClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: hordeHTTPTimeout},
	}
}

func (c *hordeHTTPClient) SubmitJob(ctx context.Context, job *buildgraph.BuildGraphJob) (string, error) {
	body, err := json.Marshal(map[string]string{
		"name":   job.Name,
		"target": job.Target,
	})
	if err != nil {
		return "", fmt.Errorf("marshaling job request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/jobs", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "ServiceAccount "+c.token)
	}

	resp, err := c.http.Do(req) //nolint:gosec // URL sourced from provisioned instance state, not user input
	if err != nil {
		return "", fmt.Errorf("connecting to Horde at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("Horde rejected the request (auth): check admin token in .fabrica/horde-credentials.yaml")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound && c.jobsRouteMissing(ctx) {
			return "", fmt.Errorf("Horde has no job-creation API: POST %s/api/v1/jobs returned HTTP 404. This Horde build does not expose the jobs/graphs/agents API surface, so it cannot accept jobs. Rebuild the Horde AMI with a server image that includes the job API (see docs/horde-ami.md), or use a supported Horde version.", c.baseURL)
		}
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("Horde returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	return result.ID, nil
}

// jobsRouteMissing reports whether the Horde server provably lacks the
// job-creation route: GET /api/v1/jobs must itself return 404. A transport
// error, an unbuildable request, or any other status means we cannot confirm
// the controller is absent, so the original 404 is reported verbatim.
func (c *hordeHTTPClient) jobsRouteMissing(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/jobs", nil)
	if err != nil {
		return false
	}
	if c.token != "" {
		req.Header.Set("Authorization", "ServiceAccount "+c.token)
	}

	resp, err := c.http.Do(req) //nolint:gosec // URL sourced from provisioned instance state, not user input
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusNotFound
}

func (c *hordeHTTPClient) GetJobStatus(ctx context.Context, jobID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/jobs/"+jobID, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "ServiceAccount "+c.token)
	}

	resp, err := c.http.Do(req) //nolint:gosec // URL sourced from provisioned instance state, not user input
	if err != nil {
		return "", fmt.Errorf("connecting to Horde at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("Horde returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	var result struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	return result.State, nil
}
