package ami

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The agent role bakes the stock Horde Linux agent under the native agent
// contract that internal/horde's TLS cloud-init verifies (docs/horde-tls.md,
// "Native agent AMI"): a loaded horde-agent.service running as a non-root
// user, plus the versioned marker. Paths and the unit name must stay in
// lockstep with that script.
//
// The stock agent is published framework-dependent (no native executable), as
// HordeAgent.zip from /api/v1/agentsoftware/default/zip. The unit runs it as
// `dotnet HordeAgent.dll` on the .NET 10 runtime (Horde 5.8.4 source and
// Epic's Engine/Source/Programs/Horde/Docs/Deployment/Agent.md).
const (
	roleCoordinator = "coordinator"
	roleAgent       = "agent"

	agentUser         = "horde-agent"
	agentHome         = "/var/lib/horde-agent"
	agentInstallDir   = "/opt/horde-agent"
	agentDotnet       = "/usr/bin/dotnet"
	agentAssembly     = agentInstallDir + "/HordeAgent.dll"
	agentDataDir      = agentInstallDir + "/Data"
	agentShareDir     = agentHome + "/.local/share"
	agentUnitName     = "horde-agent.service"
	agentUnitPath     = "/etc/systemd/system/" + agentUnitName
	agentContractPath = "/etc/horde/fabrica-native-agent-contract"
)

// agentWritablePaths are the only paths the service user may write. The unit
// lists them as ReadWritePaths under ProtectSystem=strict.
var agentWritablePaths = []string{agentDataDir, agentShareDir}

var (
	agentArtifactRE = regexp.MustCompile(`^s3://[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]/[A-Za-z0-9._/-]+\.zip$`)
	sha256RE        = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// validateAgentArtifact checks the operator-supplied zip location and pin.
// The URI and digest are interpolated into the bake script, so the accepted
// character set is deliberately narrow: nothing that needs shell quoting.
func validateAgentArtifact(uri, sha string) error {
	switch {
	case uri == "":
		return errors.New("--agent-artifact is required with --role agent: upload the stock Horde agent HordeAgent.zip to S3 and pass its s3:// URI (see docs/horde-agent-ami.md)")
	case !agentArtifactRE.MatchString(uri):
		return fmt.Errorf("--agent-artifact must be an s3:// URI ending in .zip (e.g. s3://my-bucket/horde/HordeAgent.zip); got %q. Horde 5.8.4 publishes the agent as a zip, not a .tar.gz", uri)
	case sha == "":
		return errors.New("--agent-sha256 is required with --role agent: run sha256sum on HordeAgent.zip and pass the 64-character digest; there is no default hash")
	case !sha256RE.MatchString(sha):
		return fmt.Errorf("--agent-sha256 must be 64 hexadecimal characters (a SHA-256 digest); got %q. Run sha256sum on HordeAgent.zip and pass the hash field", sha)
	}
	return nil
}

// agentBakeScript installs the agent: fetch and verify the zip, install the
// .NET 10 runtime it runs on, create the service user and writable paths,
// write the unit and contract marker, and enable the unit without starting it.
// Shared by the Image Builder component and the Packer template so both bake
// identical artifacts.
func agentBakeScript(artifact, sha string) string {
	return strings.Join([]string{
		agentFetchScript(artifact, sha),
		dotnetRuntimeInstallScript(),
		agentInstallScript(),
	}, "\n")
}

// agentFetchScript installs libicu70 and unzip, downloads the zip with the
// full-path AWS CLI, and checks it against the pinned SHA-256 before anything
// is unpacked. The archive is extracted as root with no group or other write
// bits.
func agentFetchScript(artifact, sha string) string {
	return strings.Join([]string{
		"set -euo pipefail",
		"apt-get update -q",
		"apt-get install -y libicu70 unzip",
		`work="$(mktemp -d)"`,
		`trap 'rm -rf "$work"' EXIT`,
		`/usr/local/bin/aws s3 cp "` + artifact + `" "$work/HordeAgent.zip"`,
		`printf '%s  %s\n' "` + strings.ToLower(sha) + `" "$work/HordeAgent.zip" > "$work/HordeAgent.sha256"`,
		`sha256sum -c "$work/HordeAgent.sha256"`,
		"install -d -m 0755 " + agentInstallDir,
		`unzip -q "$work/HordeAgent.zip" -d ` + agentInstallDir,
		"chown -R root:root " + agentInstallDir,
		"chmod -R go-w " + agentInstallDir,
	}, "\n")
}

// agentInstallScript creates the nologin service user, its two writable
// directories, the systemd unit, and the contract marker, then enables the
// unit. It never starts the service: the bake must not run an agent.
func agentInstallScript() string {
	return strings.Join([]string{
		"id -u " + agentUser + " >/dev/null 2>&1 || useradd --system --home-dir " + agentHome + " --no-create-home --shell /usr/sbin/nologin " + agentUser,
		"install -d -o root -g root -m 0755 " + agentHome + " " + agentHome + "/.local",
		"install -d -o " + agentUser + " -g " + agentUser + " -m 0700 " + agentShareDir,
		"install -d -o " + agentUser + " -g " + agentUser + " -m 0750 " + agentDataDir,
		"cat >" + agentUnitPath + " <<'UNIT'",
		agentUnitText(),
		"UNIT",
		"install -d -m 0755 /etc/horde",
		`printf '1\n' > ` + agentContractPath,
		"systemctl daemon-reload",
		"systemctl enable " + agentUnitName,
	}, "\n")
}

// agentUnitText is the horde-agent.service unit: a non-root service user with
// no privilege escalation and a read-only filesystem except the two writable
// paths. The agent runs from its data directory, where the Horde-written
// servers.json and caches live.
func agentUnitText() string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Unreal Horde Build Agent",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"User=" + agentUser,
		"WorkingDirectory=" + agentDataDir,
		"ExecStart=" + agentDotnet + " " + agentAssembly + " -WorkingDir=" + agentDataDir,
		"Environment=DOTNET_NOLOGO=1",
		"Environment=DOTNET_CLI_TELEMETRY_OPTOUT=1",
		"Restart=always",
		"RestartSec=10",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ReadWritePaths=" + strings.Join(agentWritablePaths, " "),
		"StandardOutput=journal",
		"StandardError=journal",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
	}, "\n")
}

// agentValidateScript asserts the baked agent meets the contract. It runs as
// root and checks the service user's access the way the unit will see it.
func agentValidateScript() string {
	return strings.Join([]string{
		"set -euo pipefail",
		"test -f " + agentAssembly,
		"test -x " + agentDotnet,
		agentDotnet + " --list-runtimes | grep -q '^Microsoft.NETCore.App 10\\.'",
		`test "$(cat ` + agentContractPath + `)" = 1`,
		`test "$(systemctl show ` + agentUnitName + ` --property=User --value)" = ` + agentUser,
		"systemctl is-enabled " + agentUnitName,
		"! systemctl is-active --quiet " + agentUnitName,
		"runuser -u " + agentUser + " -- test -w " + agentDataDir,
		"runuser -u " + agentUser + " -- test -w " + agentShareDir,
		"! runuser -u " + agentUser + " -- test -w " + agentAssembly,
	}, "\n")
}
