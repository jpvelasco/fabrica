package cloud

import "context"

// StateBackendReconcileSpec carries an approved existing-resource snapshot and
// trusted setup tags. Expected comes from ownership classification; Tags comes
// from protected merging, never directly from operator input.
type StateBackendReconcileSpec struct {
	Expected StateBackendMetadata
	Tags     map[string]string
}

// StateBackendReconciler revalidates existence and the protected owner/marker
// snapshot before updating an existing backend. It preserves fresh unrelated
// tags and existing markers, never creates resources, and does not classify
// ownership. A changed snapshot requires a fresh full preflight.
type StateBackendReconciler interface {
	ReconcileStateBucket(ctx context.Context, bucket string, spec StateBackendReconcileSpec) error
	ReconcileStateLockTable(ctx context.Context, table string, spec StateBackendReconcileSpec) error
}
