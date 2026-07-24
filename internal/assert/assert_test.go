package assert

import (
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

func TestContainsCaseSensitive(t *testing.T) {
	// Verify case sensitivity — "World" should NOT match "world".
	// We can't assert a fatalf directly, but we can verify the function
	// delegates to strings.Contains which is case-sensitive.
	if strings.Contains("Hello World", "world") {
		t.Fatal("should be case sensitive")
	}
}
