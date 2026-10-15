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

// NoneOf verifies that s contains none of substrs, failing the test with
// which one was found.
func NoneOf(t T, s string, substrs ...string) {
	t.Helper()
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			t.Fatalf("%q\nshould not contain\n%q", s, sub)
		}
	}
}
