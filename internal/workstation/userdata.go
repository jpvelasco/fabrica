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
if ! echo "$DCV_USER:{{ .SessionPassword }}" | chpasswd; then
  echo "ERROR: chpasswd failed; the DCV login password was not set. Inspect /var/log/cloud-init-output.log."
  exit 1
fi

# The password above rides in EC2 UserData until this point, where any local
# process (IMDS /latest/user-data) or any principal with
# ec2:DescribeInstanceAttribute can read it. Scrub the long-lived exposure
# now that chpasswd has consumed it: clear the IMDS copy (IMDSv2 token first,
# IMDSv1 fallback) and truncate the local cloud-init copies. The script fails
# closed only if BOTH clears fail; one successful clear is enough. The
# operator record is .fabrica/workstation-credentials.yaml, not the instance.
IMDS_TOKEN=$(curl -s -X PUT -H "X-aws-ec2-metadata-token-ttl-seconds: 30" \
  http://169.254.169.254/latest/api/token)
if ! curl -s -X PUT -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" -d "" \
    http://169.254.169.254/latest/user-data \
  && ! curl -s -X PUT -d "" http://169.254.169.254/latest/user-data; then
  echo "ERROR: userdata scrub failed after chpasswd; the session password may still be reachable via IMDS user-data. Inspect the instance over SSM."
  exit 1
fi
sudo truncate -s 0 /var/lib/cloud/instance/user-data.txt 2>/dev/null
sudo truncate -s 0 /var/lib/cloud/instance/user-data 2>/dev/null
echo "Scrubbed EC2 userdata (local + IMDS)."

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
