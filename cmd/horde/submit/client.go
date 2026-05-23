package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
)

// HordeClient abstracts communication with the Horde REST API.
// The interface lives here (not in internal/) because only submit needs it in V1.
type HordeClient interface {
	SubmitJob(ctx context.Context, job *buildgraph.BuildGraphJob) (jobID string, err error)
	GetJobStatus(ctx context.Context, jobID string) (state string, err error)
}

type hordeHTTPClient struct {
	baseURL string // e.g. "http://10.0.1.42:5000"
	token   string // service account token (optional)
	http    *http.Client
}

func newHordeHTTPClient(baseURL, token string) *hordeHTTPClient {
	return &hordeHTTPClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{},
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
		return "", fmt.Errorf("Horde returned HTTP %d", resp.StatusCode)
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
		return "", fmt.Errorf("Horde returned HTTP %d", resp.StatusCode)
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
