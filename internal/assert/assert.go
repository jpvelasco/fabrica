// Package assert provides shared test assertion helpers.
package assert

import (
	"strings"
)

// T is the minimal testing interface used by this package.
// *testing.T satisfies this interface.
type T interface {
	Helper()
	Fatalf(format string, args ...any)
}

// Contains verifies that s contains substr, failing the test if not.
func Contains(t T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%q\ndoes not contain\n%q", s, substr)
	}
}
