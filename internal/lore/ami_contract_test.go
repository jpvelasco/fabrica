package lore

import (
	"strings"
	"testing"
)

func TestDefaultAMIContract(t *testing.T) {
	contract := DefaultAMIContract()

	checks := map[string]struct {
		got  string
		want string
	}{
		"base OS":      {got: contract.BaseOS, want: "Ubuntu 22.04 LTS (Jammy)"},
		"architecture": {got: contract.Architecture, want: "x86_64"},
		"binary":       {got: contract.BinaryPath, want: "/opt/loreserver/loreserver"},
		"command":      {got: contract.CommandPath, want: "/usr/local/bin/loreserver"},
		"config":       {got: contract.ConfigDir, want: DefaultConfigDir},
		"service":      {got: contract.ServiceName, want: "loreserver"},
		"health path":  {got: contract.HealthPath, want: "/health_check"},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if check.got != check.want {
				t.Errorf("%s = %q, want %q", name, check.got, check.want)
			}
		})
	}
	if contract.HTTPPort != DefaultHTTPPort {
		t.Errorf("HTTPPort = %d, want %d", contract.HTTPPort, DefaultHTTPPort)
	}
	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestAMIContractValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AMIContract)
	}{
		{
			name: "missing required value",
			mutate: func(contract *AMIContract) {
				contract.ServiceName = ""
			},
		},
		{
			name: "relative binary path",
			mutate: func(contract *AMIContract) {
				contract.BinaryPath = "opt/loreserver/loreserver"
			},
		},
		{
			name: "invalid HTTP port",
			mutate: func(contract *AMIContract) {
				contract.HTTPPort = 65536
			},
		},
		{
			name: "health path without leading slash",
			mutate: func(contract *AMIContract) {
				contract.HealthPath = "health_check"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := DefaultAMIContract()
			tt.mutate(&contract)
			if err := contract.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestAMIContractScriptsRejectInvalidContracts(t *testing.T) {
	contract := DefaultAMIContract()
	contract.HTTPPort = 0

	if _, err := contract.InstallScript("/tmp/lore-bin"); err == nil {
		t.Error("InstallScript() error = nil, want validation error")
	}
	if _, err := contract.VerificationScript(); err == nil {
		t.Error("VerificationScript() error = nil, want validation error")
	}
	if _, err := contract.BakeVerificationScript(); err == nil {
		t.Error("BakeVerificationScript() error = nil, want validation error")
	}
}

func TestAMIContractInstallScriptRejectsRelativeSource(t *testing.T) {
	if _, err := DefaultAMIContract().InstallScript("lore-bin"); err == nil {
		t.Error("InstallScript() error = nil, want absolute source directory error")
	}
}

func TestAMIContractInstallScript(t *testing.T) {
	contract := DefaultAMIContract()
	script, err := contract.InstallScript("/tmp/lore-bin")
	if err != nil {
		t.Fatalf("InstallScript() error: %v", err)
	}

	for _, want := range []string{
		"set -euo pipefail",
		// The pinned OSS tarball ships loreserver mode 0644; it must be
		// executable before the source check can pass.
		"chmod 0755 /tmp/lore-bin/loreserver",
		"cp -a /tmp/lore-bin/. /opt/loreserver/",
		"chmod 0755 /opt/loreserver/loreserver",
		"ln -sfn /opt/loreserver/loreserver /usr/local/bin/loreserver",
		"ConditionPathExists=/etc/loreserver/local.toml",
		"ExecStart=/opt/loreserver/loreserver --config /etc/loreserver",
		"systemctl enable loreserver.service",
		// The SSM agent is not guaranteed to ship as amazon-ssm-agent.service on
		// every base image; a hard requirement aborts the bake.
		"systemctl enable amazon-ssm-agent.service || systemctl enable snap.amazon-ssm-agent.amazon-ssm-agent.service || true",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script missing %q", want)
		}
	}
	for _, forbidden := range []string{"aws s3", "SECRET", "PASSWORD", "TOKEN"} {
		if strings.Contains(strings.ToUpper(script), strings.ToUpper(forbidden)) {
			t.Errorf("install script must not contain %q", forbidden)
		}
	}
}

func TestAMIContractVerificationScript(t *testing.T) {
	contract := DefaultAMIContract()
	script, err := contract.VerificationScript()
	if err != nil {
		t.Fatalf("VerificationScript() error: %v", err)
	}

	for _, want := range []string{
		"command -v loreserver",
		"systemctl is-enabled --quiet loreserver.service",
		"systemctl is-active --quiet loreserver.service",
		"curl --fail --silent --show-error http://127.0.0.1:41339/health_check",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("verification script missing %q", want)
		}
	}
}

func TestAMIContractBakeVerificationScript(t *testing.T) {
	contract := DefaultAMIContract()
	script, err := contract.BakeVerificationScript()
	if err != nil {
		t.Fatalf("BakeVerificationScript() error: %v", err)
	}

	for _, want := range []string{
		"test -x /opt/loreserver/loreserver",
		"command -v loreserver",
		"systemctl is-enabled --quiet loreserver.service",
		"unit=\"$(systemctl cat loreserver.service)\"",
		"ConditionPathExists=/etc/loreserver/local.toml",
		"ExecStart=/opt/loreserver/loreserver --config /etc/loreserver",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("bake verification script missing %q", want)
		}
	}
}
