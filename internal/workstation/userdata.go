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
}

var userDataTmpl = template.Must(template.New("userdata").Parse(`#!/bin/bash
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
`))

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
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("rendering userdata template: %w", err)
	}
	return buf.String(), nil
}
