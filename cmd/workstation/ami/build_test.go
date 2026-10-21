package ami

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildValidate(t *testing.T) {
	c := buildCommand{cfg: BuildConfig{BaseImage: "not-ami", Region: "us-west-2", Name: "x", OutputDir: "out", RootDevice: defaultRootDevice}}
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid AMI")
	}
	c.cfg.BaseImage = defaultBaseImage
	c.cfg.Region = "not-a-region"
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid region")
	}
	c.cfg.Region = "us-west-2"
	c.cfg.Name = "bad name"
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid name")
	}
}

func TestBuildWritesDCVAndSSM(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	c := buildCommand{
		out: &out,
		cfg: BuildConfig{
			BaseImage: "ami-0bdb09211df876db4",
			Region:    "us-west-2",
			Name:      "fabrica-workstation-dcv",
			OutputDir: dir,
		},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	c.cfg.RootDevice = defaultRootDevice
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	comp, err := os.ReadFile(filepath.Join(dir, "component.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(comp)
	for _, want := range []string{
		// Pinned versioned tarball (2026.0-24952) with a hard SHA256 check
		// and a fingerprint-gated GPG key import (#490).
		"https://d1uj6qtbmh3dt5.cloudfront.net/2026.0/Servers/nice-dcv-2026.0-24952-ubuntu2204-x86_64.tgz",
		"52651a93844ce3d5ad918ee7666b007bb2a1c7ff7cf68dc50692e6b18efaa35a",
		"sha256sum -c -",
		"5B9EEBC864449701F6CE566A11B5C70A170C6114",
		// Isolated throwaway keyring: show-only preview must hold exactly
		// one primary key with the pinned fingerprint before import, and
		// the keyring must hold exactly that key afterwards, so a
		// second key cannot emit an extra VALIDSIG line.
		"GNUPGHOME=\"$(mktemp -d /tmp/fabrica-gnupg.XXXXXX)\"",
		"gpg --batch --no-tty --show-keys --import-options show-only --with-colons --keyid-format long NICE-GPG-KEY",
		"gpg --batch --no-tty --yes --import NICE-GPG-KEY",
		`[ "$keyringpubs" -ne 1 ] || [ "$keyringprimary" != "$NICE_KEY_FPR" ]`,
		// Per-deb verification of the embedded _gpgbuilder signature
		// member: the gpg status stream goes to a dedicated fd 3 and must
		// contain exactly one anchored VALIDSIG whose signing-key
		// fingerprint field (first field of the line) is the pinned key;
		// any BADSIG/ERRSIG/EXPKEYSIG/REVKEYSIG/NO_PUBKEY line fails.
		// That field is the signing key's fingerprint; it equals the
		// pinned primary fingerprint because the pinned NICE key has no
		// signing subkey (the primary signs directly).
		// The manifest is parsed from gpg's verified --output (the data
		// the signature covers) and its names must equal the archive's
		// ar members exactly, in order: exactly 3 members plus
		// _gpgbuilder, no duplicates, no unlisted members, no '/' in
		// any name, no '..' path component — all checked from the
		// member list BEFORE ar extraction — and the three members must
		// be exactly debian-binary, control.tar.*, data.tar.* in that
		// order. The gpg status capture uses "|| rc=$?" so the specific
		// error message and cleanup still run under set -e.
		"verify_deb",
		`gpg --batch --no-tty --status-fd 3 --verify "$work/_gpgbuilder" >"$work/vout" 2>"$work/verr" 3>"$vstat" || rc=$?`,
		`arnames="$( ( cd "$work" && ar t "$abs" ) 2>/dev/null || true)"`,
		`grep -qE '(^|/)\.\.(/|$)'`,
		"member name containing a '..' path component in",
		`( cd "$work" && ar x "$abs" )`,
		`gpg --batch --no-tty --status-fd 3 --verify --output "$work/manifest" "$work/_gpgbuilder"`,
		`wc -c < "$work/$name" | awk '{print $1}'`,
		`grep -Ec '^\[GNUPG:\] VALIDSIG '`,
		`(BADSIG|ERRSIG|EXPKEYSIG|REVKEYSIG|NO_PUBKEY)`,
		`sed 's/^\[GNUPG:\] VALIDSIG //' | awk '{print $1}'`,
		`[ "$validsig_fpr" != "$NICE_KEY_FPR" ]`,
		// Manifest member names are bound to the exact deb layout, in
		// order: debian-binary, control.tar.*, data.tar.*.
		"first manifest member of",
		"debian-binary",
		`^control\.tar\.`,
		`^data\.tar\.`,
		// binutils provides ar for the _gpgbuilder extraction;
		// x11-xserver-utils provides xsetroot and dbus-x11 provides
		// dbus-launch on jammy (plain dbus does not); both are required
		// for the DCV virtual session to survive boot (#489). xterm is
		// the terminal emulator Xsession requires when no session
		// manager, window manager, or user ~/.xsession is present (#492).
		"apt-get install -y wget gnupg binutils xvfb x11-xserver-utils dbus-x11 xdg-user-dirs xterm",
		`dcvdir=$(find . -maxdepth 1 -type d -name 'nice-dcv-*' | head -n1)`,
		"systemctl enable dcvserver",
		"command -v dcv",
		"command -v xsetroot",
		"command -v dbus-launch",
		"command -v xterm",
		"snap install amazon-ssm-agent --classic",
		"amazon-ssm-agent.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("component missing %q", want)
		}
	}
	// The unversioned "latest" tarball URL must not remain — the build is
	// pinned to the 2026.0-24952 release path.
	if strings.Contains(s, "https://d1uj6qtbmh3dt5.cloudfront.net/nice-dcv-ubuntu2204-x86_64.tgz") {
		t.Error("component must not use the unversioned DCV tarball URL")
	}
	// The SSM section must not soft-fail. ("|| true" inside the GPG
	// preflight command substitutions is intentional: every captured
	// value is strictly validated by the comparison that follows, so a
	// failed capture fails closed.)
	i := strings.Index(s, "EnsureSSMAgent")
	if i < 0 {
		t.Fatal("component missing EnsureSSMAgent step")
	}
	ssm := s[i:]
	if strings.Contains(ssm, "|| true") {
		t.Error("component must not soft-fail SSM")
	}
	// dpkg-sig is no longer the verification path: jammy's
	// dpkg-sig 0.13.1 rejects the control.tar.zst format every 2026.0
	// deb uses (BADSIG rc=2 on all seven correctly-signed debs), so the
	// check runs directly against the _gpgbuilder member with GPG.
	if strings.Contains(s, "dpkg-sig --verify") {
		t.Error("component must not use dpkg-sig --verify (broken on control.tar.zst on jammy)")
	}
	recipe, err := os.ReadFile(filepath.Join(dir, "image-builder-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recipe), `"uninstallAfterBuild": false`) {
		t.Error("recipe must keep SSM agent after bake")
	}
}

func TestIndent(t *testing.T) {
	got := indent(2, "a\nb\n")
	if got != "  a\n  b" {
		t.Errorf("indent = %q", got)
	}
}

func TestBuildValidateEmptyFields(t *testing.T) {
	c := buildCommand{cfg: BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "", OutputDir: "out", RootDevice: defaultRootDevice}}
	if err := c.validate(); err == nil {
		t.Fatal("expected empty name error")
	}
	c.cfg.Name = "ok"
	c.cfg.OutputDir = ""
	if err := c.validate(); err == nil {
		t.Fatal("expected empty output-dir error")
	}
}

func TestBuildMkdirError(t *testing.T) {
	c := buildCommand{
		out:      io.Discard,
		cfg:      BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "x", OutputDir: "out", RootDevice: defaultRootDevice},
		mkdirAll: func(string, os.FileMode) error { return errors.New("mkdir boom") },
	}
	err := c.run()
	if err == nil || !strings.Contains(err.Error(), "creating --output-dir") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildWriteError(t *testing.T) {
	c := buildCommand{
		out:       io.Discard,
		cfg:       BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "x", OutputDir: t.TempDir(), RootDevice: defaultRootDevice},
		mkdirAll:  os.MkdirAll,
		writeFile: func(string, []byte, os.FileMode) error { return errors.New("write boom") },
	}
	err := c.run()
	if err == nil || !strings.Contains(err.Error(), "writing") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderMissingTemplate(t *testing.T) {
	c := buildCommand{}
	_, err := c.render("no-such.tmpl", struct{}{})
	if err == nil || !strings.Contains(err.Error(), "reading template") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderExecuteError(t *testing.T) {
	c := buildCommand{}
	_, err := c.render("component.yaml.tmpl", struct{}{})
	if err == nil || !strings.Contains(err.Error(), "rendering template") {
		t.Fatalf("got %v", err)
	}
}
