package workstation

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// UserDataConfig is the input shape for the DCV cloud-init script.
type UserDataConfig struct {
	SessionPassword    string // required; used for the DCV session
	IdleTimeoutMinutes int    // defaults to DefaultIdleTimeoutMinutes
	MountPerforce      bool   // install p4 CLI and write ~/.p4config
	PerforceServerAddr string // host:port of the Perforce server (e.g. 10.0.1.5:1666)
}

var userDataTmpl = template.Must(template.New("userdata").Option("missingkey=error").Parse(`#!/bin/bash
set -euo pipefail

# Install NICE DCV server
snap install --classic dcv-server 2>/dev/null || apt-get install -y dcv-server

# Configure NICE DCV
dcv configure-session --type=virtual --storage-root /home/ubuntu/dcv
dcv configure --idle-timeout={{ .IdleTimeoutMinutes }}

# Create a persistent DCV session
dcv create-session --type=virtual --storage-root /home/ubuntu/dcv workstation

# Set DCV session password (non-interactive auth)
echo "dcv:{{ .SessionPassword }}" | chpasswd

systemctl enable dcvsessionmgr dcv-session-manager-agent 2>/dev/null || true
systemctl restart dcvsessionmgr 2>/dev/null || true
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
{{ end }}`))

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	raw, err := GenerateRaw(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if cfg.SessionPassword == "" {
		return "", fmt.Errorf("SessionPassword must not be empty")
	}
	if cfg.IdleTimeoutMinutes <= 0 {
		cfg.IdleTimeoutMinutes = DefaultIdleTimeoutMinutes
	}
	if cfg.MountPerforce && cfg.PerforceServerAddr == "" {
		return "", fmt.Errorf("PerforceServerAddr must not be empty when MountPerforce is true")
	}
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("rendering userdata template: %w", err)
	}
	return buf.String(), nil
}
