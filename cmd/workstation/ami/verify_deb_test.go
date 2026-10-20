package ami

// Executed verification of the rendered component's _gpgbuilder gates. The
// key-import gate lines and the verify_deb() function are extracted verbatim
// from the rendered component.yaml and executed under bash against debs
// crafted in a temp dir with throwaway GPG keys. Every negative case must be
// rejected (nonzero exit + ERROR message); the positive must pass:
//
//   - duplicated member (4 ar members, one name repeated)
//   - extra member (5 ar members)
//   - wrong-key signature with the pin embedded in the signed cleartext as a
//     non-tab decoy line (only the VALIDSIG signing-key fingerprint
//     discriminates)
//   - 1-bit tamper of a signed member (the GPG layer or the per-member
//     digest gate catches it)
//   - multi-key keyfile (the pre-import show-only gate refuses it)
//   - multi-key keyring (the post-import keyring gate refuses it)
//   - one positive: every check passes
//
// The bash crafter mirrors the manual container harness (throwaway rsa2048
// keys, clearsigned composite v4 manifests, ar-built archives). Skipped —
// not failed — only when bash, gpg, ar, md5sum, or sha1sum are absent; CI
// installs gnupg + binutils on all three test legs so the test runs there.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// winDrivePath matches a leading Windows drive prefix (e.g. "C:/") so the
// path can be rewritten into the /c/... POSIX form msys tools expect.
var winDrivePath = regexp.MustCompile(`^[A-Za-z]:[/\\]`)

// toPosixPath converts a Windows path (C:\Users\...) to the /c/Users/... POSIX
// form that Git Bash's msys gpg understands. The whole path is normalized to
// forward slashes first: os.TempDir() and filepath.Join on Windows use
// backslashes, so a prefix-only swap would yield the mixed /c:\... form that
// msys gpg rejects.
func toPosixPath(p string) string {
	return winDrivePath.ReplaceAllStringFunc(filepath.ToSlash(p), func(m string) string {
		return "/" + strings.ToLower(m[:1]) + "/"
	})
}

// gpgEnv returns the environment every gpg invocation in this test runs with:
// GNUPGHOME (in the platform-native form, so msys gpg on Windows gets a
// /c/... path). GNUPGHOME is an environment variable, so it is passed to gpg
// literally — MSYS2 argument conversion only rewrites argv, never env values.
func gpgEnv(gpgHome string) []string {
	return append(os.Environ(), "GNUPGHOME="+gpgHome)
}

// runEnv is the environment the gate/verify_deb bash scripts run with: the gpg
// env plus the pinned fingerprint the gate asserts against.
func runEnv(gpgHome, pin string) []string {
	return append(gpgEnv(gpgHome), "NICE_KEY_FPR="+pin)
}

// craftScript builds the negative and positive debs plus the throwaway keys.
// It runs once, before any gate execution. $1 is the stage dir; GNUPGHOME is
// inherited from the environment (Go sets it so the gpg home sits outside the
// stage, keeping the gpg-agent socket path short enough for macOS, and in
// POSIX form for Git Bash's msys gpg on Windows).
const craftScript = `set -euo pipefail
stage="$1"
gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Correct Builder <correct@example.com>' rsa2048 sign never
gpg --batch --pinentry-mode loopback --passphrase '' --quick-generate-key 'Decoy Builder <decoy@example.com>' rsa2048 sign never
pin="$(gpg --batch --with-colons --list-keys correct@example.com | awk -F: '$1 == "fpr" {print $10; exit}')"
cd "$stage"
mkdir members
printf '2.0\n' > members/debian-binary
printf 'ctrl' > members/control.tar.zst
printf 'data1' > members/data.tar.zst
digests() {
  for f in debian-binary control.tar.zst data.tar.zst; do
    m="$(md5sum "members/$f" | awk '{print $1}')"
    s="$(sha1sum "members/$f" | awk '{print $1}')"
    z="$(wc -c < "members/$f" | awk '{print $1}')"
    printf '\t%s %s %s %s\n' "$m" "$s" "$z" "$f"
  done
}
manifest() {
  {
    echo 'Version: 4'
    echo 'Signer: Test Builder <builder@example.com>'
    echo 'Date: 20260101T000000Z'
    echo 'Role: builder'
    echo 'Files: 3'
    if [ -n "${1:-}" ]; then printf '%s\n' "$1"; fi
    digests
  }
}
sign_with() {
  gpg --batch --pinentry-mode loopback --passphrase '' --local-user "$1" --clearsign --no-armor --output "$2" "$3"
}
manifest '' > manifest.good
manifest "DecoyFingerprint: $pin" > manifest.decoy
sign_with 'correct@example.com' sig.good manifest.good
sign_with 'decoy@example.com' sig.decoy manifest.decoy
# good: 3 members + correct-key signature
mkdir st-good && cp sig.good st-good/_gpgbuilder && cp members/debian-binary members/control.tar.zst members/data.tar.zst st-good/
( cd st-good && ar q ../good.deb debian-binary control.tar.zst data.tar.zst _gpgbuilder )
# dup: control.tar.zst repeated, data.tar.zst absent (4 members, duplicate)
mkdir st-dup && cp sig.good st-dup/_gpgbuilder && cp members/debian-binary members/control.tar.zst st-dup/
( cd st-dup && ar q ../dup.deb debian-binary control.tar.zst control.tar.zst _gpgbuilder )
# extra: an unlisted fifth member
mkdir st-extra && cp sig.good st-extra/_gpgbuilder && cp members/debian-binary members/control.tar.zst members/data.tar.zst st-extra/
printf 'x' > st-extra/extra-extra
( cd st-extra && ar q ../extra.deb debian-binary control.tar.zst data.tar.zst extra-extra _gpgbuilder )
# wrongkey: decoy-key signature; the pin is embedded in the signed cleartext
# as a non-tab decoy line, so only the VALIDSIG fingerprint discriminates.
mkdir st-wrongkey && cp sig.decoy st-wrongkey/_gpgbuilder && cp members/debian-binary members/control.tar.zst members/data.tar.zst st-wrongkey/
( cd st-wrongkey && ar q ../wrongkey.deb debian-binary control.tar.zst data.tar.zst _gpgbuilder )
# bitflip: 1-bit tamper of data.tar.zst (same size) after the signature was
# made; the GPG layer or the per-member digest gate must catch it.
mkdir st-bitflip && cp sig.good st-bitflip/_gpgbuilder && cp members/debian-binary members/control.tar.zst st-bitflip/
printf 'Xata1' > st-bitflip/data.tar.zst
( cd st-bitflip && ar q ../bitflip.deb debian-binary control.tar.zst data.tar.zst _gpgbuilder )
`

func TestVerifyDebExecutesAgainstCraftedDebs(t *testing.T) {
	for _, tool := range []string{"bash", "gpg", "ar", "md5sum", "sha1sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not available; executed verify_deb test skipped (CI installs gnupg + binutils on all test legs)")
		}
	}
	comp := mustRenderComponent(t)
	const ind = "              " // the component's fixed 14-space YAML indent
	// End anchors are the region's own closer at the base indent (full line,
	// so a more-indented nested closer never ends the region).
	gatePre := dedentSh(t, extractRegion(t, comp,
		regexp.QuoteMeta(ind+"keycheck="), regexp.QuoteMeta(ind+"fi")))
	gatePost := dedentSh(t, extractRegion(t, comp,
		regexp.QuoteMeta(ind+"keyringcheck="), regexp.QuoteMeta(ind+"fi")))
	// verify_deb region is the function itself, from its definition to its
	// own closing brace (NOT the for-loop that follows it, which would make
	// each per-deb call run the whole loop again).
	verify := dedentSh(t, extractRegion(t, comp,
		regexp.QuoteMeta(ind+"verify_deb() {"), regexp.QuoteMeta(ind+"}")))

	stage := t.TempDir()
	gpgHome := shortGPGHome(t)
	craftPath := filepath.Join(stage, "craft.sh")
	if err := os.WriteFile(craftPath, []byte(craftScript), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", craftPath, stage)
	// AR_NO_AUTOLINK: macOS binutils ar appends a __.SYMDEF SORTED
	// symbol-table member to archives it creates, which would break the
	// exact member-count check; GNU ar (linux) ignores it.
	cmd.Env = append(gpgEnv(gpgHome), "AR_NO_AUTOLINK=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crafting debs: %v:\n%s", err, out)
	}

	pin := keyFPR(t, gpgHome, "correct@example.com")
	runBash := func(script, dir string) (int, string) {
		cmd := exec.Command("bash", "-c", script)
		cmd.Dir = dir
		cmd.Env = runEnv(gpgHome, pin)
		out, err := cmd.CombinedOutput()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatalf("running %q: %v", script, err)
		}
		return 0, string(out)
	}
	verifyDeb := func(name string) (int, string) {
		return runBash("set -euo pipefail\n"+verify+"\nverify_deb "+name+"\n", stage)
	}
	assertReject := func(name, wantErr string) {
		t.Helper()
		rc, out := verifyDeb(name)
		if rc == 0 {
			t.Errorf("negative %s: verify_deb passed (must reject);\n%s", name, out)
			return
		}
		if !strings.Contains(out, "ERROR") {
			t.Errorf("negative %s: rejected without an ERROR message;\n%s", name, out)
		}
		if wantErr != "" && !strings.Contains(out, wantErr) {
			t.Errorf("negative %s: expected %q in output;\n%s", name, wantErr, out)
		}
	}
	assertReject("dup.deb", "duplicate member names")
	assertReject("extra.deb", "exactly 3 ar members plus _gpgbuilder")
	assertReject("wrongkey.deb", "is not the pinned NICE key")
	assertReject("bitflip.deb", "")
	if rc, out := verifyDeb("good.deb"); rc != 0 {
		t.Errorf("positive good.deb: verify_deb failed (rc=%d);\n%s", rc, out)
	}

	// Keyfile gates run in stage (the gate reads ./NICE-GPG-KEY) against the
	// throwaway keyring: a multi-key keyfile must be refused before import,
	// a single-key keyfile must pass the show-only preview, and a keyring
	// holding a second primary must be refused after import.
	exportKey := func(user string) []byte {
		cmd := exec.Command("gpg", "--batch", "--yes", "--export", user)
		cmd.Env = gpgEnv(gpgHome)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("exporting %s: %v", user, err)
		}
		return out
	}
	single := exportKey("correct@example.com")
	multi := append(append([]byte{}, single...), exportKey("decoy@example.com")...)
	if err := os.WriteFile(filepath.Join(stage, "NICE-GPG-KEY"), multi, 0644); err != nil {
		t.Fatal(err)
	}
	if rc, out := runBash("set -euo pipefail\n"+gatePre+"\n", stage); rc == 0 || !strings.Contains(out, "refusing to import") {
		t.Errorf("multi-key keyfile: pre-import gate did not refuse (rc=%d);\n%s", rc, out)
	}
	if err := os.WriteFile(filepath.Join(stage, "NICE-GPG-KEY"), single, 0644); err != nil {
		t.Fatal(err)
	}
	if rc, out := runBash("set -euo pipefail\n"+gatePre+"\n", stage); rc != 0 {
		t.Errorf("single-key keyfile: pre-import gate rejected (rc=%d);\n%s", rc, out)
	}
	// The throwaway keyring already holds both keys, so the post-import
	// keyring gate must refuse it (keyringpubs=2).
	if rc, out := runBash("set -euo pipefail\n"+gatePost+"\n", stage); rc == 0 || !strings.Contains(out, "does not hold exactly the pinned") {
		t.Errorf("multi-key keyring: post-import gate did not refuse (rc=%d);\n%s", rc, out)
	}
}

// shortGPGHome returns a GNUPGHOME with a short path, removed by t.Cleanup.
// The gpg-agent socket ($GNUPGHOME/S.gpg-agent) must stay under 104 chars;
// macOS's per-user $TMPDIR base is already ~68, so on darwin the home is
// created under /tmp instead. On Windows the PATH gpg is Git Bash's msys
// build, which mangles raw C:\... paths, so the home is returned in
// /c/Users/... POSIX form and only ever crosses process boundaries via the
// environment.
func shortGPGHome(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	home, err := os.MkdirTemp(base, "fvdb-*")
	if err != nil {
		t.Fatalf("gpg home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if runtime.GOOS == "windows" {
		home = toPosixPath(home)
	}
	return home
}

func keyFPR(t *testing.T, gpgHome, user string) string {
	t.Helper()
	cmd := exec.Command("gpg", "--batch", "--with-colons", "--list-keys", user)
	cmd.Env = gpgEnv(gpgHome)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing key %s: %v", user, err)
	}
	for _, l := range strings.Split(string(out), "\n") {
		// Colon format: fpr:<8 empty fields>:<40-hex-fpr>; the fpr is field 10
		// (index 9), matching the gate's awk '{print $10}' extraction.
		f := strings.Split(l, ":")
		if len(f) >= 10 && f[0] == "fpr" {
			return f[9]
		}
	}
	t.Fatalf("no fpr line for %s", user)
	return ""
}

func mustRenderComponent(t *testing.T) string {
	t.Helper()
	body, err := buildCommand{}.render("component.yaml.tmpl", struct {
		BuildConfig
		EnsureSSM  string
		RequireSSM string
	}{EnsureSSM: "echo ensure", RequireSSM: "echo require"})
	if err != nil {
		t.Fatalf("rendering component template: %v", err)
	}
	return string(body)
}

// extractRegion returns the lines from the line starting with start (first
// occurrence) through the line matching end (first occurrence at or after
// start), inclusive. start is a line prefix — the anchored region begins
// mid-line (e.g. `keycheck="$(gpg ...)"`); end is a full line — the region's
// own closer, which is never a prefix.
func extractRegion(t *testing.T, comp, start, end string) string {
	t.Helper()
	reStart := regexp.MustCompile("^" + start)
	reEnd := regexp.MustCompile("^" + end + "$")
	lines := strings.Split(comp, "\n")
	startIdx := -1
	for i, l := range lines {
		if reStart.MatchString(l) {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
		t.Fatalf("start line %q not found in rendered component", start)
	}
	for i := startIdx; i < len(lines); i++ {
		if reEnd.MatchString(lines[i]) {
			return strings.Join(lines[startIdx:i+1], "\n") + "\n"
		}
	}
	t.Fatalf("end line %q not found after start line %q", end, start)
	return ""
}

// dedentSh strips the component's fixed 14-space YAML indent so the extracted
// shell runs at column 0; it fails if a non-blank line had less than 14
// leading spaces.
func dedentSh(t *testing.T, region string) string {
	t.Helper()
	space14 := strings.Repeat(" ", 14)
	var out []string
	for _, l := range strings.Split(region, "\n") {
		if l == "" {
			out = append(out, l)
			continue
		}
		if !strings.HasPrefix(l, space14) {
			t.Fatalf("extracted shell line lost its 14-space indent: %q", l)
		}
		out = append(out, l[len(space14):])
	}
	return strings.Join(out, "\n")
}
