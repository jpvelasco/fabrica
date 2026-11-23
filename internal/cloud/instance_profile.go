package cloud

import "context"

// InstanceProfileInspector reads a pre-created IAM instance profile before a
// module references it. Errors wrap ErrResourceNotFound (missing) or
// ErrInspectionDenied (IAM denied the read).
type InstanceProfileInspector interface {
	GetInstanceProfile(ctx context.Context, name string) (InstanceProfileInfo, error)
}

// InstanceProfileInfo is the subset of an IAM instance profile Fabrica checks.
type InstanceProfileInfo struct {
	Name  string
	Roles []string
}
