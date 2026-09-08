package teardown

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

const (
	// HintEmptyBucket is the next step after a non-empty S3 delete failure.
	HintEmptyBucket = "Empty the bucket, then re-run destroy. Run 'fabrica drift' to list Extra leftovers."
	// HintPartial is the next step after a generic or partial destroy failure.
	HintPartial = "Run 'fabrica drift' to list Extra leftovers, then retry destroy. The state backend is preserved so remaining managed resources stay tracked."
)

// LeftoverHint returns a single next-step line after a failed or partial destroy.
// Human output only — callers must skip this when JSONOut is set.
func LeftoverHint(err error) string {
	if looksLikeNonEmptyBucket(err) {
		return HintEmptyBucket
	}
	return HintPartial
}

func looksLikeNonEmptyBucket(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, cloud.ErrStateBucketNotEmpty) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "not empty") || strings.Contains(msg, "AWS::S3::Bucket")
}

func (c Command) printLeftoverHint(err error) {
	if c.JSONOut || err == nil {
		return
	}
	fmt.Fprintln(c.Out, LeftoverHint(err))
}
