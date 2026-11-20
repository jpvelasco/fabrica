package ami

import (
	"strings"
	"testing"
)

const v086Digest = "f0de84c6175a476f157754f57316be0346105be502c93fabb65bb908eab0e1e1"

func TestPinnedLoreSHA256(t *testing.T) {
	got, err := pinnedLoreSHA256("0.8.6")
	if err != nil {
		t.Fatalf("pinnedLoreSHA256(0.8.6) error: %v", err)
	}
	if got != v086Digest {
		t.Errorf("pinnedLoreSHA256(0.8.6) = %q, want %q", got, v086Digest)
	}
}

func TestPinnedLoreSHA256RejectsUnpinnedVersions(t *testing.T) {
	for _, version := range []string{"0.8.5", "5.5.0", "latest", "0.8", ""} {
		t.Run(version, func(t *testing.T) {
			_, err := pinnedLoreSHA256(version)
			if err == nil {
				t.Fatalf("pinnedLoreSHA256(%q) expected error, got nil", version)
			}
			for _, want := range []string{"supported versions: 0.8.6", "loreTarballSHA256", "lore_checksums.go"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestDefaultLoreVersionIsPinned(t *testing.T) {
	if _, err := pinnedLoreSHA256(defaultLoreVersion); err != nil {
		t.Fatalf("defaultLoreVersion %q must be pinned: %v", defaultLoreVersion, err)
	}
}
