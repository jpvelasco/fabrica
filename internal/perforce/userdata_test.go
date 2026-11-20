package perforce

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/userdata"
)

func TestGenerateRaw_LatestVersion(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "latest", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "helix-p4d=") {
		t.Error("latest version should not pin a version in apt-get install")
	}
	if !strings.Contains(got, "apt-get install -y helix-p4d") {
		t.Error("missing 'apt-get install -y helix-p4d'")
	}
}

// TestGenerateRaw_PinnedVersionFailsClosed pins #699: a pin resolves to an
// exact repo version and never falls back to an unpinned install.
func TestGenerateRaw_PinnedVersionFailsClosed(t *testing.T) {
	for _, tc := range []struct{ version, pin string }{
		{"2025.2", "2025.2"},
		{"2024.2/3084003", "2024.2/3084003"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			got, err := GenerateRaw(UserDataConfig{Version: tc.version, AdminPass: "testpass"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range []string{
				`HELIX_PIN="$(echo '` + tc.pin + `' | tr / -)"`,
				"apt-cache madison helix-p4d",
				`if [ -z "$HELIX_DEB_VERSION" ]; then`,
				"ERROR: Helix Core " + tc.pin + " is not in the Perforce apt repo",
				`apt-get install -y "helix-p4d=$HELIX_DEB_VERSION"`,
				`echo "Installed helix-p4d $(dpkg-query -W -f='${Version}' helix-p4d)"`,
			} {
				if !strings.Contains(got, want) {
					t.Errorf("pinned userdata missing %q", want)
				}
			}
			errIdx := strings.Index(got, "is not in the Perforce apt repo")
			if exitIdx := strings.Index(got[errIdx:], "exit 1"); errIdx < 0 || exitIdx < 0 {
				t.Error("an unavailable pin must exit 1")
			}
			for _, banned := range []string{"falling back", "apt-get install -y helix-p4d\n", `"helix-p4d=` + tc.pin + `"`} {
				if strings.Contains(got, banned) {
					t.Errorf("pinned userdata must not contain %q", banned)
				}
			}
		})
	}
}

// TestGenerateRaw_AdminPasswordLiteralOnce verifies the raw password literal
// appears exactly once (the ADMIN_PASS assignment); both generation branches
// reference the variable instead of repeating the secret.
func TestGenerateRaw_AdminPasswordLiteralOnce(t *testing.T) {
	pass := "s3cr3tP@ssw0rd"
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: pass})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count := strings.Count(got, pass); count != 1 {
		t.Errorf("admin password appears %d times, want exactly 1", count)
	}
	if !strings.Contains(got, `ADMIN_PASS="`+pass+`"`) {
		t.Error("password assignment missing")
	}
	if !strings.Contains(got, `"$ADMIN_PASS"`) {
		t.Error("configure branches must reference $ADMIN_PASS")
	}
}

// TestGenerateRaw_ScrubsUserDataAfterConfigure verifies the admin password
// still reaches configure exactly once, and the long-lived local UserData
// copies are truncated only after configure and the service start have run.
// The scrub is local-only: IMDS is read-only to the instance, so the script
// must not contact the metadata service (#489).
func TestGenerateRaw_ScrubsUserDataAfterConfigure(t *testing.T) {
	pass := "s3cr3tP@ssw0rd"
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: pass})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count := strings.Count(got, pass); count != 1 {
		t.Errorf("admin password appears %d times, want exactly 1 (the ADMIN_PASS assignment)", count)
	}
	for _, want := range []string{
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt",
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt.i",
		"truncate -s 0 /var/lib/cloud/instance/user-data\n",
		"test -s /var/lib/cloud/instance/user-data; then",
		"ERROR: userdata scrub failed after configure",
		"Scrubbed EC2 userdata (local).",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("scrub block missing %q", want)
		}
	}
	for _, gone := range []string{"169.254.169.254", "curl", "IMDS_TOKEN"} {
		if strings.Contains(got, gone) {
			t.Errorf("scrub must not contact IMDS; found %q", gone)
		}
	}

	configureIdx := strings.Index(got, `--super-passwd "$ADMIN_PASS"`)
	pinnedIdx := strings.Index(got, `-P "$ADMIN_PASS"`)
	helixIdx := strings.Index(got, "systemctl restart helix-p4d")
	p4dctlIdx := strings.Index(got, `p4dctl start "$SERVER_ID"`)
	scrubIdx := strings.Index(got, "truncate -s 0 /var/lib/cloud/instance/user-data.txt")
	if configureIdx < 0 || pinnedIdx < 0 || helixIdx < 0 || p4dctlIdx < 0 || scrubIdx < 0 ||
		configureIdx > scrubIdx || pinnedIdx > scrubIdx || helixIdx > scrubIdx || p4dctlIdx > scrubIdx {
		t.Error("userdata must configure and start the server before scrubbing user-data")
	}

	errIdx := strings.Index(got, "ERROR: userdata scrub failed after configure")
	okIdx := strings.Index(got, "Scrubbed EC2 userdata (local).")
	if errIdx < 0 || okIdx < errIdx {
		t.Fatal("scrub error and success lines missing or out of order")
	}
	// The scrub's own exit 1 must sit between its error line and the success
	// line. (Earlier error paths also exit 1 — the first one after the scrub
	// block's error would be the pin-fallback exit if it came before the
	// re-verify, so search the error..success region explicitly.)
	if exitRel := strings.Index(got[errIdx:okIdx], "exit 1"); exitRel < 0 {
		t.Error("scrub failure must exit 1 between the error line and the success line")
	}
	// part-001 is the file cloud-init is executing, so the wipe must be the
	// last statement of the script — nothing may follow it (#490).
	part := "truncate -s 0 /var/lib/cloud/instance/scripts/part-001"
	if !strings.Contains(got, part) {
		t.Fatal("part-001 wipe missing from rendered userdata")
	}
	if !strings.HasSuffix(strings.TrimRight(got, " \t\r\n"), part) {
		t.Error("part-001 must be the last statement of the rendered script — cloud-init is executing that file")
	}
}

func TestGenerateRaw_DataDeviceAutoDetection(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "pw"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"findmnt -n -o SOURCE /",
		"lsblk -no PKNAME",
		"no unformatted data volume found besides root",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("auto-detection missing %q", want)
		}
	}
	if !strings.Contains(got, "mkfs.ext4 \"$DATA_DEVICE\"") {
		t.Error("mkfs must target the detected device variable")
	}
}

// TestGenerateRaw_ExplicitDataDeviceHonored verifies an explicit device skips
// auto-detection and is used verbatim.
func TestGenerateRaw_ExplicitDataDeviceHonored(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "pw", DataDevice: "/dev/nvme2n1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `"DATA_DEVICE=/dev/nvme2n1"`) && !strings.Contains(got, "DATA_DEVICE=\"/dev/nvme2n1\"") {
		t.Errorf("explicit device not honored:\n%s", got)
	}
}

// TestGenerateRaw_RuntimeInterfaceDetection verifies the configure/service
// steps detect the Perforce packaging generation at runtime rather than
// render time — the installed version can differ from the requested pin when
// the archive dropped it.
func TestGenerateRaw_RuntimeInterfaceDetection(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "pw"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		`grep -q -- '--super-passwd'`,
		"-P \"$ADMIN_PASS\"",
		`--super-passwd "$ADMIN_PASS"`,
		`grep -q '^helix-p4d'`,
		"p4dctl start \"$SERVER_ID\"",
		"systemctl restart helix-p4d",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("runtime detection missing %q", want)
		}
	}
}

func TestGenerateRaw_MountPoint(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "/hxdepots") {
		t.Error("expected mount point '/hxdepots'")
	}
}

// TestGenerateRaw_NeverXtrace pins item 2 of the #490 security review: the
// rendered script must never enable shell xtrace (set -x / set +x), or the
// embedded admin password would leak into cloud-init-output.log.
func TestGenerateRaw_NeverXtrace(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "s3cr3tP@ssw0rd"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.NoneOf(t, got, "set -x", "set +x", "set -e -x", "set -ex", "set -xe", "xtrace")
}

func TestGenerateRaw_PipefailPresent(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Error("expected 'set -euo pipefail'")
	}
}

func TestGenerateRaw_EmptyAdminPassError(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPass: ""})
	if err == nil {
		t.Error("expected error for empty AdminPass")
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("fills all zeros", func(t *testing.T) {
		cfg := UserDataConfig{AdminPass: "pw"}
		cfg.applyDefaults()
		if cfg.DataMount != "/hxdepots" {
			t.Errorf("DataMount = %q, want /hxdepots", cfg.DataMount)
		}
		if cfg.ServerID != "fabrica-perforce" {
			t.Errorf("ServerID = %q, want fabrica-perforce", cfg.ServerID)
		}
	})
	t.Run("preserves existing values", func(t *testing.T) {
		cfg := UserDataConfig{
			AdminPass:  "pw",
			DataDevice: "/dev/custom",
			DataMount:  "/custom",
			ServerID:   "my-server",
		}
		cfg.applyDefaults()
		if cfg.DataDevice != "/dev/custom" {
			t.Errorf("DataDevice = %q, want /dev/custom", cfg.DataDevice)
		}
		if cfg.DataMount != "/custom" {
			t.Errorf("DataMount = %q, want /custom", cfg.DataMount)
		}
		if cfg.ServerID != "my-server" {
			t.Errorf("ServerID = %q, want my-server", cfg.ServerID)
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("empty admin pass", func(t *testing.T) {
		cfg := UserDataConfig{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for empty AdminPass")
		}
		if !strings.Contains(err.Error(), "AdminPass") {
			t.Errorf("error %q should mention AdminPass", err.Error())
		}
	})
	t.Run("valid config", func(t *testing.T) {
		cfg := UserDataConfig{AdminPass: "secret"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestGenerate_ValidationError(t *testing.T) {
	_, err := Generate(UserDataConfig{AdminPass: ""})
	if err == nil {
		t.Fatal("expected error for empty AdminPass")
	}
}

func TestGenerate_ReturnsBase64(t *testing.T) {
	got, err := Generate(UserDataConfig{Version: "2025.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// base64 strings only contain A-Z, a-z, 0-9, +, /, =
	for _, c := range got {
		if !isBase64Char(c) {
			t.Errorf("Generate returned non-base64 character %q in output", c)
			break
		}
	}
}

func isBase64Char(c rune) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
}

func TestGenerate_DefaultsApplied(t *testing.T) {
	got, err := Generate(UserDataConfig{Version: "latest", AdminPass: "pass"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), "fabrica-perforce") {
		t.Error("default ServerID should appear in decoded output")
	}
}

func TestGenerateRaw_PerforceAptKeyringPinned(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "latest", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, userdata.PerforceAptRepoShell())
	assert.NoneOf(t, got, "apt-key", "focal", "http://package.perforce.com")
}
