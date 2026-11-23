package instanceprofile

import "testing"

func TestNameOrPrefersReferencedName(t *testing.T) {
	if got := (Ref{Name: "workload"}).NameOr("fabrica-x"); got != "workload" {
		t.Errorf("NameOr(external) = %q, want workload", got)
	}
	if got := (Ref{}).NameOr("fabrica-x"); got != "fabrica-x" {
		t.Errorf("NameOr(managed) = %q, want fallback fabrica-x", got)
	}
}
