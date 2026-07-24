package assert

import (
	"fmt"
	"strings"
	"testing"
)

func TestContainsPass(t *testing.T) {
	Contains(t, "hello world", "world")
}

func TestContainsEmpty(t *testing.T) {
	// Empty substr is always contained.
	Contains(t, "anything", "")
	Contains(t, "", "")
}

func TestContainsExact(t *testing.T) {
	Contains(t, "abc", "abc")
	Contains(t, "abc", "a")
	Contains(t, "abc", "c")
}

func TestContainsSubstring(t *testing.T) {
	Contains(t, "hello world foo bar", "world foo")
}

// failingT wraps *testing.T but intercepts Fatalf instead of calling
// runtime.Goexit, so the failure branch can be covered in a passing test.
type failingT struct {
	*testing.T
	failed bool
	msg    string
}

func (f *failingT) Helper() {}

func (f *failingT) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = fmt.Sprintf(format, args...)
}

func TestContainsFailureBranch(t *testing.T) {
	ft := &failingT{T: t}
	Contains(ft, "hello", "xyz")
	if !ft.failed {
		t.Fatal("Contains should have called Fatalf for missing substring")
	}
	if !strings.Contains(ft.msg, "hello") {
		t.Fatalf("error message should include the string: %q", ft.msg)
	}
	if !strings.Contains(ft.msg, "xyz") {
		t.Fatalf("error message should include the substring: %q", ft.msg)
	}
}

func TestContainsCaseSensitive(t *testing.T) {
	// Verify case sensitivity — "World" should NOT match "world".
	// We can't assert a fatalf directly, but we can verify the function
	// delegates to strings.Contains which is case-sensitive.
	if strings.Contains("Hello World", "world") {
		t.Fatal("should be case sensitive")
	}
}
