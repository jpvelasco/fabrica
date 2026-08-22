package provision

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestWaitIntervalReturnsAfterDuration verifies an uncancelled wait completes.
func TestWaitIntervalReturnsAfterDuration(t *testing.T) {
	start := time.Now()
	if err := WaitInterval(context.Background(), 5*time.Millisecond); err != nil {
		t.Fatalf("WaitInterval: %v", err)
	}
	if time.Since(start) < 5*time.Millisecond {
		t.Error("WaitInterval returned before the interval elapsed")
	}
}

// TestWaitIntervalCancelledContext verifies cancellation ends the wait
// immediately with the context error — the whole point of the helper.
func TestWaitIntervalCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := WaitInterval(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Error("cancelled wait did not return promptly")
	}
}
