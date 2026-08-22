package provision

import (
	"context"
	"time"
)

// WaitInterval waits for d, returning early with the context error when ctx is
// cancelled. Polling commands use this as their interval seam so Ctrl+C ends
// the wait immediately instead of after the full interval.
func WaitInterval(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
