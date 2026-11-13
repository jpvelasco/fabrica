package cloud

import "context"

// StateBackendMetadata describes actual backend resource tags. A successful
// read distinguishes a missing resource (Exists false) from an existing,
// untagged resource (Exists true, empty Tags). Tags are not inferred from names.
type StateBackendMetadata struct {
	Exists bool
	Tags   map[string]string
}

// StateBackendMetadataReader inspects backend resources without changing them.
// Unreadable or malformed metadata returns an error, never proof of absence.
type StateBackendMetadataReader interface {
	StateBucketMetadata(ctx context.Context, bucket string) (StateBackendMetadata, error)
	StateLockTableMetadata(ctx context.Context, table string) (StateBackendMetadata, error)
}
