package destroy

import (
	"strings"
	"testing"
)

// The plan must not claim a referenced profile or role is deleted: destroy keeps
// a perforce.instanceProfile the platform team pre-created.
func TestIrreversibleTextKeepsReferencedProfile(t *testing.T) {
	text := spec.Irreversible
	if strings.Contains(text, "security group, IAM profile/role, and") {
		t.Errorf("Irreversible = %q, must not claim the IAM profile/role is always deleted", text)
	}
	if !strings.Contains(text, "perforce.instanceProfile") {
		t.Errorf("Irreversible = %q, want it to say a pre-created perforce.instanceProfile is kept", text)
	}
}
