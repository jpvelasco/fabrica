// Package cloud defines provider-agnostic interfaces and shared constants
// used across plan layers and cost estimators.
package cloud

import "context"

// S3BucketCleaner empties versioned S3 buckets so teardown can delete them.
// Modules with S3-backed storage (e.g. the Lore store bucket) must purge every
// object version and delete marker before the bucket itself is removed;
// otherwise Cloud Control deletion fails with BucketNotEmpty forever.
type S3BucketCleaner interface {
	// PurgeBucket deletes all current object versions, noncurrent versions,
	// and delete markers in the named bucket.
	PurgeBucket(ctx context.Context, bucket string) error
}
