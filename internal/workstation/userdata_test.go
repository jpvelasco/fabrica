package workstation

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

func TestGenerateRawRequiresSessionPassword(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error when SessionPassword is empty")
	}
	assert.Contains(t, err.Error(), "SessionPassword")
}

func TestGenerateRawRequiresDCVOnAMI(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"command -v dcv",
		"NICE DCV is not installed on this AMI",
		"hunter2",
		"systemctl enable dcvserver",
		"systemctl restart dcvserver",
	} {
		assert.Contains(t, got, want)
	}
	if strings.Contains(got, "snap install") || strings.Contains(got, "apt-get install -y dcv-server") {
		t.Error("userdata must not install DCV at boot; the AMI is DCV-first")
	}
	if strings.Contains(got, "|| true") {
		t.Error("userdata must not soft-fail DCV enable")
	}
}

func TestGenerateRawUsesCurrentDCVCLI(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// DCV 2025.0.x removed configure-session/configure; the script must not
	// call them (under set -euo pipefail that aborts before session creation).
	for _, gone := range []string{"configure-session", "dcv configure "} {
		if strings.Contains(got, gone) {
			t.Errorf("userdata must not call removed DCV CLI command %q: #438", gone)
		}
	}
	for _, want := range []string{
		"dcv set-config --section connectivity --key idle-timeout",
		"hunter2",
		"chpasswd",
		"dcv list-sessions",
		"ERROR: DCV session 'workstation' did not appear",
	} {
		assert.Contains(t, got, want)
	}
	// create-session spans two lines with a backslash continuation; join the
	// continuation and assert the full command line.
	joined := strings.ReplaceAll(got, `
`+"\n", " ")
	assert.Contains(t, joined, "dcv create-session --type virtual --user \"$DCV_USER\" --owner \"$DCV_USER\"")
	assert.Contains(t, joined, "--storage-root /home/\"$DCV_USER\"/dcv workstation")

	// With dcvserver stopped, 'dcv create-session' exits 0 but the session is
	// never persisted — the daemon must be started before the session is
	// created (verified live on a DCV 2025.0.x instance; #438). The command
	// spans a backslash-continuation line, so the full CLI line is 'joined'.
	restartIdx := strings.Index(got, "systemctl restart dcvserver")
	createIdx := strings.Index(joined, "dcv create-session --type virtual")
	if restartIdx < 0 || createIdx < 0 || restartIdx > createIdx {
		t.Error("dcvserver must be restarted before 'dcv create-session'; with the daemon stopped the session is not persisted (#438)")
	}
}

func TestGenerateRawScrubsUserDataLast(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// The session password still rides through chpasswd so the DCV login
	// works (#443 keeps the single-credential model)...
	assert.Contains(t, got, `echo "$DCV_USER:hunter2" | chpasswd`)
	// ...but the long-lived local UserData copies are scrubbed last:
	// truncated, re-verified empty, and fail-closed if a survivor remains.
	// The scrub is local-only — IMDS /latest/user-data is read-only to the
	// instance, so the script must not contact the metadata service (#489).
	for _, want := range []string{
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt",
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt.i",
		"truncate -s 0 /var/lib/cloud/instance/user-data\n",
		"test -s /var/lib/cloud/instance/user-data; then",
		// part-001 is the file cloud-init is executing; it is wiped last so
		// the last live local copy of the embedded password is removed (#490).
		"truncate -s 0 /var/lib/cloud/instance/scripts/part-001",
		"ERROR: userdata scrub failed after chpasswd",
		"Scrubbed EC2 userdata (local).",
	} {
		assert.Contains(t, got, want)
	}
	for _, gone := range []string{"169.254.169.254", "curl", "IMDS_TOKEN"} {
		if strings.Contains(got, gone) {
			t.Errorf("scrub must not contact IMDS; found %q", gone)
		}
	}
	// Order: chpasswd first, then dcvserver starts and the session is created,
	// and only then does the scrub run — so nothing follows the part-001 wipe.
	chpassIdx := strings.Index(got, "chpasswd")
	dcvIdx := strings.Index(got, "systemctl restart dcvserver")
	scrubIdx := strings.Index(got, "truncate -s 0 /var/lib/cloud/instance/user-data.txt")
	partIdx := strings.Index(got, "truncate -s 0 /var/lib/cloud/instance/scripts/part-001")
	if chpassIdx < 0 || dcvIdx < 0 || scrubIdx < 0 || partIdx < 0 ||
		chpassIdx > dcvIdx || dcvIdx > scrubIdx || scrubIdx > partIdx {
		t.Error("userdata must chpasswd, start dcvserver, then scrub user-data with part-001 last (#443, #490)")
	}
	// part-001 is the file cloud-init is executing, so the wipe must be the
	// last statement of the rendered script — nothing may follow it (#490).
	part := "truncate -s 0 /var/lib/cloud/instance/scripts/part-001"
	if !strings.HasSuffix(strings.TrimRight(got, " \t\r\n"), part) {
		t.Error("part-001 must be the last statement of the rendered script — cloud-init is executing that file")
	}
	// The scrub is fail-closed: a surviving local copy must abort cloud-init
	// (the exit 1 lives between the re-verify and the success line).
	if strings.Count(got, "exit 1") < 5 {
		t.Errorf("userdata must fail closed on scrub failure; got %q", got)
	}
}

// TestGenerateRawNeverXtrace pins item 2 of the #490 security review: the
// rendered script must never enable shell xtrace (set -x / set +x), or the
// embedded session password would leak into cloud-init-output.log.
func TestGenerateRawNeverXtrace(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2", MountPerforce: true, PerforceServerAddr: "10.0.1.5:1666"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.NoneOf(t, got, "set -x", "set +x", "set -e -x", "set -ex", "set -xe", "xtrace")
}

func TestGenerateRawChpasswdFailClosed(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.Contains(t, got, "ERROR: chpasswd failed")
	// chpasswd must run inside an if-condition (fail closed), not as a
	// bare line where set -e only would mask a partial failure.
	idx := strings.Index(got, "chpasswd")
	if idx < 0 {
		t.Fatal("chpasswd line missing")
	}
	line := got[idx-40 : idx+20]
	if !strings.Contains(line, "if !") {
		t.Errorf("chpasswd must be guarded by 'if !' for a fail-closed error line, got: %q", line)
	}
}

func TestGenerateRawIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		IdleTimeoutMinutes: 30,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.Contains(t, got, "30")
}

func TestGenerateProducesValidBase64(t *testing.T) {
	b64, err := Generate(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(decoded) == 0 {
		t.Error("decoded userdata is empty")
	}
}

func TestGenerateRawDefaultIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.Contains(t, got, "60")
}

func TestGenerateRawMountPerforceRequiresAddr(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{
		SessionPassword: "pw",
		MountPerforce:   true,
		// PerforceServerAddr intentionally empty
	})
	if err == nil {
		t.Fatal("expected error when MountPerforce=true and PerforceServerAddr is empty")
	}
	assert.Contains(t, err.Error(), "PerforceServerAddr")
}

func TestGenerateRawMountPerforceInjectsP4Config(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		MountPerforce:      true,
		PerforceServerAddr: "10.0.1.5:1666",
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"helix-cli",
		"p4config",
		"10.0.1.5:1666",
		"P4PORT",
	} {
		assert.Contains(t, got, want)
	}
}

func TestGenerateRawNoMountPerforceNoP4Block(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if strings.Contains(got, "helix-cli") {
		t.Error("without --mount-perforce, userdata must not contain helix-cli")
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("fills zero idle timeout", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw"}
		cfg.applyDefaults()
		if cfg.IdleTimeoutMinutes != DefaultIdleTimeoutMinutes {
			t.Errorf("IdleTimeoutMinutes = %d, want %d", cfg.IdleTimeoutMinutes, DefaultIdleTimeoutMinutes)
		}
	})
	t.Run("preserves existing idle timeout", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", IdleTimeoutMinutes: 45}
		cfg.applyDefaults()
		if cfg.IdleTimeoutMinutes != 45 {
			t.Errorf("IdleTimeoutMinutes = %d, want 45", cfg.IdleTimeoutMinutes)
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("empty session password", func(t *testing.T) {
		cfg := UserDataConfig{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for empty SessionPassword")
		}
		assert.Contains(t, err.Error(), "SessionPassword")
	})
	t.Run("mount perforce without addr", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", MountPerforce: true}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for MountPerforce without PerforceServerAddr")
		}
		assert.Contains(t, err.Error(), "PerforceServerAddr")
	})
	t.Run("valid config", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("valid config with perforce", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", MountPerforce: true, PerforceServerAddr: "10.0.1.5:1666"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestGenerate_ValidationError(t *testing.T) {
	_, err := Generate(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error for empty SessionPassword")
	}
}
