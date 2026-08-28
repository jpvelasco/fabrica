package lore

import (
	"fmt"
	"path"
	"strings"
)

// AMIContract defines the operating-system surface that a Lore AMI provides to
// Fabrica's runtime cloud-init.
type AMIContract struct {
	BaseOS       string
	Architecture string
	BinaryPath   string
	CommandPath  string
	ConfigDir    string
	ServiceName  string
	HTTPPort     int
	HealthPath   string
}

// DefaultAMIContract returns the supported production Lore AMI contract.
func DefaultAMIContract() AMIContract {
	return AMIContract{
		BaseOS:       "Ubuntu 22.04 LTS (Jammy)",
		Architecture: "x86_64",
		BinaryPath:   "/opt/loreserver/loreserver",
		CommandPath:  "/usr/local/bin/loreserver",
		ConfigDir:    DefaultConfigDir,
		ServiceName:  "loreserver",
		HTTPPort:     DefaultHTTPPort,
		HealthPath:   "/health_check",
	}
}

// Validate verifies that the contract can safely generate bake artifacts.
func (c AMIContract) Validate() error {
	for name, value := range map[string]string{
		"base OS":          c.BaseOS,
		"architecture":     c.Architecture,
		"binary path":      c.BinaryPath,
		"command path":     c.CommandPath,
		"config directory": c.ConfigDir,
		"service name":     c.ServiceName,
		"health path":      c.HealthPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("AMI contract %s is required", name)
		}
	}
	if !path.IsAbs(c.BinaryPath) || !path.IsAbs(c.CommandPath) || !path.IsAbs(c.ConfigDir) {
		return fmt.Errorf("AMI contract binary, command, and config paths must be absolute")
	}
	if c.HTTPPort <= 0 || c.HTTPPort > 65535 {
		return fmt.Errorf("AMI contract HTTP port %d is invalid", c.HTTPPort)
	}
	if !strings.HasPrefix(c.HealthPath, "/") {
		return fmt.Errorf("AMI contract health path %q must start with /", c.HealthPath)
	}
	return nil
}

// InstallScript returns the builder-independent installation script. sourceDir
// must contain the licensed Lore distribution with loreserver at its root.
func (c AMIContract) InstallScript(sourceDir string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	if !path.IsAbs(sourceDir) {
		return "", fmt.Errorf("Lore source directory %q must be absolute", sourceDir)
	}
	binaryDir := path.Dir(c.BinaryPath)
	serviceUnit := fmt.Sprintf("/etc/systemd/system/%s.service", c.ServiceName)
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

# The pinned OSS tarball ships loreserver mode 0644, so it must be made
# executable before the source check can pass.
chmod 0755 %[1]s/loreserver
test -x %[1]s/loreserver
install -d -m 0755 %[2]s %[3]s
cp -a %[1]s/. %[2]s/
chmod 0755 %[4]s
ln -sfn %[4]s %[5]s
cat > %[6]s <<'UNIT'
[Unit]
Description=Epic Lore server
After=network-online.target
Wants=network-online.target
ConditionPathExists=%[3]s/local.toml

[Service]
Type=simple
ExecStart=%[4]s --config %[3]s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable %[7]s.service
# The SSM agent is not guaranteed to ship as amazon-ssm-agent.service on
# every base image; a hard requirement aborts the bake.
systemctl enable amazon-ssm-agent.service || systemctl enable snap.amazon-ssm-agent.amazon-ssm-agent.service || true
`, sourceDir, binaryDir, c.ConfigDir, c.BinaryPath, c.CommandPath, serviceUnit, c.ServiceName), nil
}

// VerificationScript returns the post-provisioning verification script. Run it
// only after Fabrica's cloud-init has written the Lore configuration.
func (c AMIContract) VerificationScript() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

test -x %[1]s
command -v loreserver >/dev/null
test "$(readlink -f "$(command -v loreserver)")" = %[1]s
test -f %[2]s/local.toml
systemctl is-enabled --quiet %[3]s.service
systemctl is-active --quiet %[3]s.service
curl --fail --silent --show-error http://127.0.0.1:%[4]d%[5]s
`, c.BinaryPath, c.ConfigDir, c.ServiceName, c.HTTPPort, c.HealthPath), nil
}

// BakeVerificationScript returns the verification script that builders run
// before creating an AMI. It does not require a runtime Lore configuration.
func (c AMIContract) BakeVerificationScript() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

test -x %[1]s
command -v loreserver >/dev/null
test "$(readlink -f "$(command -v loreserver)")" = %[1]s
systemctl is-enabled --quiet %[2]s.service
unit="$(systemctl cat %[2]s.service)"
printf '%%s\n' "$unit" | grep -Fqx 'ConditionPathExists=%[3]s/local.toml'
printf '%%s\n' "$unit" | grep -Fqx 'ExecStart=%[1]s --config %[3]s'
`, c.BinaryPath, c.ServiceName, c.ConfigDir), nil
}
