package root_test

import (
	"slices"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
)

func TestMissingReleaseTag(t *testing.T) {
	const tag = "v0.4.3"
	ok := "Current stable: v0.4.3 (2026-08-23)."
	stale := "Current stable: v0.3.2 (2026-04-01)."

	tests := []struct {
		name    string
		readme  string
		roadmap string
		tag     string
		want    []string
	}{
		{
			name:    "match",
			readme:  ok,
			roadmap: ok,
			tag:     tag,
			want:    nil,
		},
		{
			name:    "README missing",
			readme:  stale,
			roadmap: ok,
			tag:     tag,
			want:    []string{"README.md"},
		},
		{
			name:    "ROADMAP missing",
			readme:  ok,
			roadmap: stale,
			tag:     tag,
			want:    []string{"ROADMAP.md"},
		},
		{
			name:    "both missing",
			readme:  stale,
			roadmap: stale,
			tag:     tag,
			want:    []string{"README.md", "ROADMAP.md"},
		},
		{
			name:    "empty tag",
			readme:  stale,
			roadmap: stale,
			tag:     "",
			want:    nil,
		},
		{
			name:    "invalid tag",
			readme:  stale,
			roadmap: stale,
			tag:     "null",
			want:    nil,
		},
		{
			name:    "whitespace tag",
			readme:  stale,
			roadmap: stale,
			tag:     "   ",
			want:    nil,
		},
		{
			name:    "draft-looking tag still checked",
			readme:  ok,
			roadmap: ok,
			tag:     "v0.5.0-rc.1",
			want:    []string{"README.md", "ROADMAP.md"},
		},
		{
			name:    "draft-looking tag present",
			readme:  "Current stable: v0.5.0-rc.1",
			roadmap: "Current stable: v0.5.0-rc.1",
			tag:     "v0.5.0-rc.1",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := root.MissingReleaseTag(tt.readme, tt.roadmap, tt.tag)
			if !slices.Equal(got, tt.want) {
				t.Errorf("MissingReleaseTag(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
