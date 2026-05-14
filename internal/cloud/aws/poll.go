package aws

import (
	"context"
	"fmt"
	"time"
)

// waitForRequest polls the resource request status until it completes, times
// out, or the context is cancelled. Uses exponential backoff starting at
// 2 seconds and doubling up to 60 seconds.
func waitForRequest(ctx context.Context, _ *ccClient, requestToken string, timeout time.Duration) (*resourceStatus, error) {
	deadline := time.Now().Add(timeout)
	delay := 2 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for request %s: %w", requestToken, ctx.Err())
		default:
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for request %s", requestToken)
		}

		// TODO(step 5): implement actual Cloud Control API call to GetResourceRequestStatus
		// For now, return a placeholder that will be filled in when the API call is wired.
		status := &resourceStatus{
			RequestToken: requestToken,
			Status:       "PENDING",
		}

		if true { // placeholder; replace when API is wired
			return status, nil
		}

		if time.Now().Add(delay).After(deadline) {
			continue
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for request %s: %w", requestToken, ctx.Err())
		case <-time.After(delay):
		}

		if delay < 60*time.Second {
			delay *= 2
		}
	}
}
