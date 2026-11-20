package ami

import (
	"fmt"
	"sort"
	"strings"
)

// loreTarballSHA256 pins the SHA-256 of the x86_64-unknown-linux-gnu loreserver
// tarball for each Lore release that "lore ami build" will bake. Copy each digest
// from the GitHub release asset's digest field, not from a local download. The
// rendered component verifies the tarball against this digest before extracting.
var loreTarballSHA256 = map[string]string{
	"0.8.6": "f0de84c6175a476f157754f57316be0346105be502c93fabb65bb908eab0e1e1",
}

// pinnedLoreSHA256 returns the pinned tarball digest for version. Versions
// without a pin are refused so an unverified tarball never reaches the component.
func pinnedLoreSHA256(version string) (string, error) {
	if digest, ok := loreTarballSHA256[version]; ok {
		return digest, nil
	}
	return "", fmt.Errorf("--lore-version %q has no pinned SHA-256, so fabrica will not bake it (supported versions: %s); pick a supported version, or upgrade fabrica to one that pins this release", version, supportedLoreVersions())
}

func supportedLoreVersions() string {
	versions := make([]string, 0, len(loreTarballSHA256))
	for v := range loreTarballSHA256 {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return strings.Join(versions, ", ")
}
