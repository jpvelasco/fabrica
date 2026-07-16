package prompt

import (
	"os"
	"testing"
)

// withStdin redirects os.Stdin to a pipe carrying the given input for the
// duration of fn, restoring the original afterwards. An empty input simulates
// an immediate EOF (e.g. a closed/non-interactive stdin).
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("writing input: %v", err)
	}
	w.Close()

	orig := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = orig
		r.Close()
	}()
	fn()
}

func TestConfirm(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase y", "y\n", true},
		{"uppercase Y", "Y\n", true},
		{"yes", "yes\n", true},
		{"Yes mixed case", "Yes\n", true},
		{"YES uppercase", "YES\n", true},
		{"explicit n", "n\n", false},
		{"empty line defaults to no", "\n", false},
		{"unrelated text", "maybe\n", false},
		{"eof returns false", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			withStdin(t, tc.input, func() { got = Confirm("proceed?") })
			if got != tc.want {
				t.Errorf("Confirm(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestConfirmExact(t *testing.T) {
	const phrase = "delete perforce"
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"exact match", "delete perforce\n", true},
		{"match with surrounding whitespace", "  delete perforce  \n", true},
		{"partial match", "delete\n", false},
		{"case mismatch", "Delete Perforce\n", false},
		{"empty line", "\n", false},
		{"eof returns false", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			withStdin(t, tc.input, func() { got = ConfirmExact("type the phrase", phrase) })
			if got != tc.want {
				t.Errorf("ConfirmExact(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
