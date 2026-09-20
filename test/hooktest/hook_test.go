// Package hooktest regression-tests the .githooks/pre-push hook's coverage
// plumbing on every platform the repo builds on (CI runs ubuntu, windows,
// and macos).
//
// The bug (#440): the hook created its coverage profile with `mktemp`, which
// under Git Bash yields a POSIX /tmp/... path. The Windows `go` binary then
// resolved that path as <repo>\tmp\... and failed with "The system cannot
// find the path specified", so the hook reported "tests failed" on green
// code. The fix pins the profile to a repo-relative file name, which the go
// binary resolves against CWD identically on all platforms.
//
// These tests pin the two properties the hook depends on:
//  1. `go test -coverprofile=<repo-relative>` succeeds and writes the file
//     (the exact command the hook runs);
//  2. `go tool cover -func` reports an uncovered function at exactly 0.0%,
//     which the hook's `awk '$NF == "0.0%"'` filter keys on.
package hooktest

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// coverLineRE matches a `go tool cover -func` row:
// <file>.go:<line>:<TAB><Func><TAB+><percent>.
var coverLineRE = regexp.MustCompile(`^(\S+\.go:\d+:\t\S+)\t+([\d.]+)%$`)

// writeScratchModule lays down a minimal module: a covered Hello and an
// uncovered Uncovered function.
func writeScratchModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module hookcheck\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o700); err != nil {
		t.Fatalf("creating pkg dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello.go"), []byte(
		"package pkg\n\nfunc Hello() string { return \"hi\" }\n\nfunc Uncovered() int { return 42 }\n",
	), 0o600); err != nil {
		t.Fatalf("writing hello.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "hello_test.go"), []byte(
		"package pkg\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) {\n\tif Hello() == \"\" {\n\t\tt.Fatal(\"empty\")\n\t}\n}\n",
	), 0o600); err != nil {
		t.Fatalf("writing hello_test.go: %v", err)
	}
	return dir
}

// TestCoverProfileRepoRelative exercises the hook's exact coverage command
// with a repo-relative profile name — the #440 regression. On a Windows go
// binary the old mktemp /tmp path failed here with "cannot find the path".
func TestCoverProfileRepoRelative(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	dir := writeScratchModule(t)

	// Repo-relative name, exactly as .githooks/pre-push now uses it.
	profile := "fabrica-prepush-cov.123.out"
	cmd := exec.Command("go", "test", "-coverpkg=./...", "-coverprofile="+profile, "./pkg")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test with repo-relative -coverprofile failed (hook would report false failure): %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, profile)); err != nil {
		t.Fatalf("coverage profile not written at repo-relative path %s: %v", profile, err)
	}
}

// TestCoverFuncReportsZeroPercent pins the 0.0% detection the hook's
// `awk '$NF == "0.0%"'` filter relies on.
func TestCoverFuncReportsZeroPercent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	dir := writeScratchModule(t)

	profile := "fabrica-prepush-cov.456.out"
	cmd := exec.Command("go", "test", "-coverpkg=./...", "-coverprofile="+profile, "./pkg")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	funcCmd := exec.Command("go", "tool", "cover", "-func="+profile)
	funcCmd.Dir = dir
	out, err := funcCmd.Output()
	if err != nil {
		t.Fatalf("go tool cover -func: %v", err)
	}

	var uncovered, covered string
	for _, line := range strings.Split(string(out), "\n") {
		m := coverLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name, pct := m[1], m[2]
		switch pct {
		case "0.0":
			if uncovered != "" {
				t.Fatalf("two functions at 0.0%%: %q and %q (output: %s)", uncovered, name, out)
			}
			uncovered = name
		case "100.0":
			covered = name
		}
	}
	if !strings.Contains(uncovered, "Uncovered") {
		t.Errorf("0.0%% function = %q, want the Uncovered func (output: %s)", uncovered, out)
	}
	if !strings.Contains(covered, "Hello") {
		t.Errorf("100.0%% function = %q, want Hello (output: %s)", covered, out)
	}
}
