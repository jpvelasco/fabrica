package cloud

import "context"

// InstanceProfileInspector reads a pre-created IAM instance profile before a
// module references it. Create preflights through it so a missing or unreadable
// profile fails before anything is created. Errors wrap ErrResourceNotFound when
// the profile does not exist and ErrInspectionDenied when IAM denies the read.
type InstanceProfileInspector interface {
	GetInstanceProfile(ctx context.Context, name string) (InstanceProfileInfo, error)
}

// InstanceProfileInfo is the subset of an IAM instance profile Fabrica checks.
type InstanceProfileInfo struct {
	Name string
	// Roles holds the names of the roles attached to the profile.
	Roles []string
}
