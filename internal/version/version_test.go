package version

import "testing"

func TestString(t *testing.T) {
	origV, origC := Version, Commit
	t.Cleanup(func() {
		Version, Commit = origV, origC
	})

	tests := []struct {
		name   string
		ver    string
		commit string
		want   string
	}{
		{name: "dev unknown", ver: "dev", commit: "unknown", want: "dev"},
		{name: "empty commit", ver: "v1.0.0", commit: "", want: "v1.0.0"},
		{name: "with commit", ver: "v1.0.0", commit: "abc1234", want: "v1.0.0 (abc1234)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			Version, Commit = tc.ver, tc.commit
			if got := String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
