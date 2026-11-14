package state

import "fmt"

// BootstrapResult describes the outcome of one bootstrapping step.
type BootstrapResult struct {
	Name    string
	Existed bool
}

func (r BootstrapResult) String() string {
	if r.Existed {
		return fmt.Sprintf("  %s existing configuration reconciled", r.Name)
	}
	return fmt.Sprintf("  created %s", r.Name)
}
