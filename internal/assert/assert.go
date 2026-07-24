// Package assert provides shared test assertion helpers.
package assert

import (
	"strings"
	"testing"
)

// Contains verifies that s contains substr, failing the test if not.
func Contains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%q\ndoes not contain\n%q", s, substr)
	}
}
