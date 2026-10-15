package workstation

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the DCV cloud-init script.
// The session password is not a field: cloud-init reads it from SSM at boot
// so the value never lands in UserData. Perforce ADMIN_PASS can adopt the
// same Parameter Store seam later.
type UserDataConfig struct {
	SessionPasswordParameter string // required; SSM name cloud-init reads
	Region                   string // required; region of that parameter
	IdleTimeoutMinutes       int    // defaults to DefaultIdleTimeoutMinutes
	MountPerforce            bool   // install p4 CLI and write ~/.p4config
	PerforceServerAddr       string // host:port of the Perforce server (e.g. 10.0.1.5:1666)
}

// The scrub must be the LAST fragment rendered: it wipes
// /var/lib/cloud/instance/scripts/part-001 (the file cloud-init is executing),
// which is only safe once nothing follows it in the script.
var userDataRenderer = userdata.New(template.Must(template.New("userdata").Option("missingkey=error").Parse(
	workstationUserDataBefore +
		workstationUserDataAfter +
		userdata.ScrubShell("chpasswd", "session password", ".fabrica/workstation-credentials.yaml"),
)))

const workstationUserDataBefore = `#!/bin/bash
set -euo pipefail

# AMI-first: NICE DCV must already be on the image. Stock Ubuntu is not enough.
if ! command -v dcv >/dev/null 2>&1; then
  echo "ERROR: NICE DCV is not installed on this AMI. Bake a DCV AMI with 'fabrica workstation ami build' (see docs/workstation-ami.md)."
  exit 1
fi

# Sessions run as the AMI's desktop user, never root.
DCV_USER=ubuntu
if ! id -u "$DCV_USER" >/dev/null 2>&1; then
  echo "ERROR: expected user '$DCV_USER' is missing on this AMI. Bake a DCV AMI with the ubuntu user present (see docs/workstation-ami.md)."
  exit 1
fi

# Session storage root must exist and be owned by the session user (DCV 2023+).
mkdir -p /home/"$DCV_USER"/dcv
chown "$DCV_USER":"$DCV_USER" /home/"$DCV_USER"/dcv

# Idle timeout via the current CLI surface (DCV 2023+ set-config).
dcv set-config --section connectivity --key idle-timeout "{{ .IdleTimeoutMinutes }}"

# The session password is a SecureString, not a UserData field. Fetch it
# and pass it to chpasswd without writing it to the script or the log.
# .fabrica/workstation-credentials.yaml remains the operator record.
if ! command -v aws >/dev/null 2>&1; then
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y awscli
fi
if ! DCV_PASS=$(aws ssm get-parameter --name "{{ .SessionPasswordParameter }}" --with-decryption --query Parameter.Value --output text --region "{{ .Region }}"); then
  echo "ERROR: failed to read the DCV session password from SSM parameter {{ .SessionPasswordParameter }}. Confirm the instance profile can ssm:GetParameter on that parameter."
  exit 1
fi
if [ -z "$DCV_PASS" ] || [ "$DCV_PASS" = "None" ]; then
  echo "ERROR: SSM parameter {{ .SessionPasswordParameter }} returned an empty DCV session password."
  exit 1
fi
if ! printf '%s:%s\n' "$DCV_USER" "$DCV_PASS" | chpasswd; then
  echo "ERROR: chpasswd failed; the DCV login password was not set. Inspect /var/log/cloud-init-output.log."
  exit 1
fi
unset DCV_PASS

`

const workstationUserDataAfter = `
# Start the DCV server before creating the session. With the daemon stopped,
# 'dcv create-session' exits 0 but the session is never persisted.
systemctl enable dcvserver
systemctl restart dcvserver
for _ in $(seq 1 6); do
  if [ "$(systemctl is-active dcvserver 2>/dev/null)" = "active" ]; then
    break
  fi
  sleep 5
done

# Create the persistent virtual DCV session owned by the session user.
# The first Xdcv launch under boot load can outlast the session starter's
# display-number read window (virtual-session-start-timeout is 30s). The
# session then shows up in list-sessions for a few seconds and is torn down.
# Re-poll 45s after the first sighting — past that window — and, if it
# vanished, close it, drop a stale X0 socket, and create it again with
# backoff. A later launch on the same host succeeds once GLX init has
# warmed. Fail closed if it never appears, or if it is still gone after
# the last attempt (#438, #493).
dcv_session_present() {
  dcv list-sessions 2>/dev/null | grep -q workstation
}

dcv_wait_for_session() {
  local i
  for i in $(seq 1 36); do
    if dcv_session_present; then
      return 0
    fi
    sleep 5
  done
  return 1
}

dcv_create_session() {
  dcv create-session --type virtual --user "$DCV_USER" --owner "$DCV_USER" \
    --storage-root /home/"$DCV_USER"/dcv workstation
}

dcv_attempt=1
dcv_max_attempts=3
while [ "$dcv_attempt" -le "$dcv_max_attempts" ]; do
  if [ "$dcv_attempt" -gt 1 ]; then
    dcv_backoff=$(( (dcv_attempt - 1) * 15 ))
    echo "DCV session 'workstation' vanished during startup; recreating after ${dcv_backoff}s (attempt ${dcv_attempt}/${dcv_max_attempts})."
    sleep "$dcv_backoff"
    # close-session is expected to fail when the starter already removed
    # the session. A non-zero status must not abort the recreate.
    if ! dcv close-session workstation >/dev/null 2>&1; then
      echo "DCV close-session: no existing workstation session to close."
    fi
    # The starter can kill Xdcv and leave the display socket behind. The
    # next virtual session then fails the same way. Only the retry path
    # removes it, after close-session.
    rm -f /tmp/.X11-unix/X0 /tmp/.X11-unix/X0.lock
  fi
  dcv_create_session
  if ! dcv_wait_for_session; then
    echo "ERROR: DCV session 'workstation' did not appear within 3m. Inspect /var/log/cloud-init-output.log and run 'dcv list-sessions' over SSM."
    exit 1
  fi
  sleep 45
  if dcv_session_present; then
    break
  fi
  if [ "$dcv_attempt" -eq "$dcv_max_attempts" ]; then
    echo "ERROR: DCV session 'workstation' was torn down after creation. Inspect /var/log/cloud-init-output.log and run 'dcv list-sessions' over SSM."
    exit 1
  fi
  dcv_attempt=$((dcv_attempt + 1))
done
{{ if .MountPerforce }}
# Install Perforce CLI
wget -qO - https://package.perforce.com/perforce.pubkey | apt-key add -
echo "deb http://package.perforce.com/apt/ubuntu focal release" > /etc/apt/sources.list.d/perforce.list
apt-get update -y && apt-get install -y helix-cli

# Write Perforce client configuration
cat > /home/ubuntu/.p4config <<'P4EOF'
P4PORT={{ .PerforceServerAddr }}
P4USER=
P4CLIENT=
P4EOF
chown ubuntu:ubuntu /home/ubuntu/.p4config
chmod 600 /home/ubuntu/.p4config

# Set P4CONFIG env globally so p4 auto-discovers it
echo 'export P4CONFIG=~/.p4config' >> /home/ubuntu/.profile
{{ end }}`

// applyDefaults fills zero-value fields with module defaults.
func (cfg *UserDataConfig) applyDefaults() {
	if cfg.IdleTimeoutMinutes <= 0 {
		cfg.IdleTimeoutMinutes = DefaultIdleTimeoutMinutes
	}
}

// validate checks required fields. Returns nil if valid.
func (cfg *UserDataConfig) validate() error {
	if cfg.SessionPasswordParameter == "" {
		return fmt.Errorf("SessionPasswordParameter must not be empty")
	}
	if cfg.Region == "" {
		return fmt.Errorf("Region must not be empty")
	}
	if cfg.MountPerforce && cfg.PerforceServerAddr == "" {
		return fmt.Errorf("PerforceServerAddr must not be empty when MountPerforce is true")
	}
	return nil
}

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(cfg.applyDefaults, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.RenderBase64(cfg)
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(cfg.applyDefaults, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.Render(cfg)
}
