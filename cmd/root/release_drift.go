package root

import "strings"

// MissingReleaseTag reports which status docs do not contain latestTag.
// An empty or invalid tag is a no-op (same as the CI job skipping when
// the repo has no GitHub Release).
func MissingReleaseTag(readmeText, roadmapText, latestTag string) []string {
	tag := strings.TrimSpace(latestTag)
	if tag == "" || tag == "null" {
		return nil
	}
	var missing []string
	if !strings.Contains(readmeText, tag) {
		missing = append(missing, "README.md")
	}
	if !strings.Contains(roadmapText, tag) {
		missing = append(missing, "ROADMAP.md")
	}
	return missing
}
