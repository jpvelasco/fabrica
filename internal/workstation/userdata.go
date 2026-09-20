package workstation

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the DCV cloud-init script.
type UserDataConfig struct {
	SessionPassword    string // required; used for the DCV session
	IdleTimeoutMinutes int    // defaults to DefaultIdleTimeoutMinutes
	MountPerforce      bool   // install p4 CLI and write ~/.p4config
	PerforceServerAddr string // host:port of the Perforce server (e.g. 10.0.1.5:1666)
}

var userDataRenderer = userdata.New(template.Must(template.New("userdata").Option("missingkey=error").Parse(`#!/bin/bash
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

# Set the session user's password (non-interactive DCV login credentials).
echo "$DCV_USER:{{ .SessionPassword }}" | chpasswd

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
dcv create-session --type virtual --user "$DCV_USER" --owner "$DCV_USER" \
  --storage-root /home/"$DCV_USER"/dcv workstation

# Poll for the session to appear. The restart is asynchronous, and an HTTPS
# 200 on 8443 is not proof this script ran to completion — the DCV server
# keeps running when cloud-init aborts. Fail closed if the session never
# shows up (#438).
found=0
for _ in $(seq 1 36); do
  if dcv list-sessions 2>/dev/null | grep -q workstation; then
    found=1
    break
  fi
  sleep 5
done
if [ "$found" -ne 1 ]; then
  echo "ERROR: DCV session 'workstation' did not appear within 3m. Inspect /var/log/cloud-init-output.log and run 'dcv list-sessions' over SSM."
  exit 1
fi
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
{{ end }}`)))

// applyDefaults fills zero-value fields with module defaults.
func (cfg *UserDataConfig) applyDefaults() {
	if cfg.IdleTimeoutMinutes <= 0 {
		cfg.IdleTimeoutMinutes = DefaultIdleTimeoutMinutes
	}
}

// validate checks required fields. Returns nil if valid.
func (cfg *UserDataConfig) validate() error {
	if cfg.SessionPassword == "" {
		return fmt.Errorf("SessionPassword must not be empty")
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
