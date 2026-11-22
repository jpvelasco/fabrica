package perforce

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/userdata"
)

func TestGenerateRaw_LatestVersion(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "latest", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
			got, err := GenerateRaw(UserDataConfig{Version: tc.version, AdminPasswordParameter: testAdminParam, Region: testRegion})
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
			if errIdx < 0 {
				t.Fatal("missing unavailable-pin error message")
			}
			installIdx := strings.Index(got, `apt-get install -y "helix-p4d=$HELIX_DEB_VERSION"`)
			exitIdx := strings.Index(got[errIdx:], "exit 1")
			if exitIdx < 0 || installIdx < 0 || errIdx+exitIdx > installIdx {
				t.Error("an unavailable pin must exit 1 before the pinned install")
			}
			for _, banned := range []string{"falling back", "apt-get install -y helix-p4d\n", `"helix-p4d=` + tc.pin + `"`} {
				if strings.Contains(got, banned) {
					t.Errorf("pinned userdata must not contain %q", banned)
				}
			}
		})
	}
}

const (
	testAdminParam = "/fabrica/perforce/0123456789abcdef0123456789abcdef/admin-password"
	testRegion     = "us-west-2"
)

// TestGenerateRaw_NoAdminPasswordInUserData pins #700: the admin password is
// not a userdata input and never reaches the rendered script. The script
// carries only the per-create parameter name and region, which cloud-init
// uses to fetch the secret at boot.
func TestGenerateRaw_NoAdminPasswordInUserData(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "ADMIN_PASS=\"") {
		t.Error("rendered script must not assign a literal admin password")
	}
	for _, want := range []string{testAdminParam, testRegion} {
		if strings.Count(got, want) == 0 {
			t.Errorf("rendered script missing %q", want)
		}
	}
}

// TestGenerateRaw_FetchesAdminPasswordFromSSM verifies the boot-time fetch:
// the AWS CLI is installed when missing, the secret is read with
// --with-decryption, and the read fails closed before configure runs.
func TestGenerateRaw_FetchesAdminPasswordFromSSM(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"command -v aws >/dev/null 2>&1 ||",
		"apt-get install -y awscli",
		`if ! ADMIN_PASS=$(aws ssm get-parameter --name "` + testAdminParam + `" --with-decryption --query Parameter.Value --output text --region "` + testRegion + `"); then`,
		"ERROR: failed to read the Helix Core admin password from SSM parameter " + testAdminParam,
		`if [ -z "$ADMIN_PASS" ] || [ "$ADMIN_PASS" = "None" ]; then`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fetch fragment missing %q", want)
		}
	}
	install := strings.Index(got, "apt-get install -y awscli")
	fetch := strings.Index(got, "aws ssm get-parameter")
	configure := strings.Index(got, `"$CONFIGURE" -n`)
	if configure < 0 {
		configure = strings.Index(got, `"$CONFIGURE" "$SERVER_ID"`)
	}
	if install < 0 || fetch < 0 || configure < 0 || install > fetch || fetch > configure {
		t.Error("the AWS CLI must install, then the password must be fetched, before configure runs")
	}
}

// TestGenerateRaw_NoLocalScrubOrIMDS: user data no longer carries the secret,
// so the scrub that truncated local copies is gone, and the script never
// contacts IMDS.
func TestGenerateRaw_NoLocalScrubOrIMDS(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.NoneOf(t, got, "truncate -s 0", "Scrubbed EC2 userdata", "169.254.169.254", "IMDS_TOKEN")
}

func TestGenerateRaw_DataDeviceAutoDetection(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion, DataDevice: "/dev/nvme2n1"})
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
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.NoneOf(t, got, "set -x", "set +x", "set -e -x", "set -ex", "set -xe", "xtrace")
}

func TestGenerateRaw_PipefailPresent(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Error("expected 'set -euo pipefail'")
	}
}

// TestValidateAdminPasswordParameter pins the fail-closed inputs: a missing,
// malformed, or shell-unsafe parameter name and an empty region are refused
// before anything is rendered. The name is interpolated into the boot script,
// so only the per-create shape is allowed.
func TestValidateAdminPasswordParameter(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  UserDataConfig
		want string
	}{
		{"empty name", UserDataConfig{Region: testRegion}, "AdminPasswordParameter"},
		{"legacy shared name", UserDataConfig{AdminPasswordParameter: "/fabrica/perforce/admin-password", Region: testRegion}, "AdminPasswordParameter"},
		{"shell metacharacters", UserDataConfig{AdminPasswordParameter: "/fabrica/perforce/0123456789abcdef0123456789abcdef/admin-password\"; reboot; #", Region: testRegion}, "AdminPasswordParameter"},
		{"empty region", UserDataConfig{AdminPasswordParameter: testAdminParam}, "Region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GenerateRaw(tc.cfg)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("fills all zeros", func(t *testing.T) {
		cfg := UserDataConfig{AdminPasswordParameter: testAdminParam, Region: testRegion}
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
			AdminPasswordParameter: testAdminParam,
			Region:                 testRegion,
			DataDevice:             "/dev/custom",
			DataMount:              "/custom",
			ServerID:               "my-server",
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
	t.Run("empty admin password parameter", func(t *testing.T) {
		cfg := UserDataConfig{Region: testRegion}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for empty AdminPasswordParameter")
		}
		if !strings.Contains(err.Error(), "AdminPasswordParameter") {
			t.Errorf("error %q should mention AdminPasswordParameter", err.Error())
		}
	})
	t.Run("valid config", func(t *testing.T) {
		cfg := UserDataConfig{AdminPasswordParameter: testAdminParam, Region: testRegion}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestGenerate_ValidationError(t *testing.T) {
	_, err := Generate(UserDataConfig{Region: testRegion})
	if err == nil {
		t.Fatal("expected error for empty AdminPasswordParameter")
	}
}

func TestGenerate_ReturnsBase64(t *testing.T) {
	got, err := Generate(UserDataConfig{Version: "2025.2", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
	got, err := Generate(UserDataConfig{Version: "latest", AdminPasswordParameter: testAdminParam, Region: testRegion})
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
	got, err := GenerateRaw(UserDataConfig{Version: "latest", AdminPasswordParameter: testAdminParam, Region: testRegion})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, userdata.PerforceAptRepoShell())
	assert.NoneOf(t, got, "apt-key", "focal", "http://package.perforce.com")
}
