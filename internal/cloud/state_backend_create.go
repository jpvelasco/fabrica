package cloud

import "context"

// StateBucketCreateSpec supplies trusted protected setup tags and the caller's
// canonical document key. It does not authorize adoption of an existing bucket.
type StateBucketCreateSpec struct {
	Region           string
	StateDocumentKey string
	Tags             map[string]string
}

// StateBackendCreator creates only after fresh metadata confirms absence, then
// verifies evidence before completing the invocation's resource. It never
// adopts an existing resource or records state/configuration. S3 create/read
// operations are not atomic; a successful create request is not an owner claim.
// Created records a successful Create request; a later error still forbids
// completion and requires a fresh setup preflight.
type StateBackendCreator interface {
	CreateStateBucket(ctx context.Context, bucket string, spec StateBucketCreateSpec) (StateBackendCreateResult, error)
	CreateStateLockTable(ctx context.Context, table string, tags map[string]string) (StateBackendCreateResult, error)
}
