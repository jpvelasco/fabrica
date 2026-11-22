package perforce

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the cloud-init script. The admin
// password is not a field: cloud-init reads it from SSM at boot (#700).
type UserDataConfig struct {
	Version                string // "latest", "2025.2", or "2024.2/2659294"
	ServerID               string // e.g. "fabrica-perforce"
	Region                 string // required; region of AdminPasswordParameter
	AdminPasswordParameter string // required; per-create SSM SecureString name cloud-init reads
	DataDevice             string // optional explicit device; empty = auto-detect (never the root disk)
	DataMount              string // "/hxdepots"
}

// The rendered script contains the parameter name and region, never the
// password. There is no local scrub: the script holds nothing secret to wipe.
var userDataRenderer = userdata.New(template.Must(template.New("userdata").Parse(
	perforceUserDataHead +
		userdata.PerforceAptRepoShell() +
		perforceUserDataBody,
)))

// perforceUserDataHead fetches the admin password before anything else runs,
// so a missing IAM grant or parameter fails the boot before any disk or
// package change. The value stays in ADMIN_PASS and reaches configure only as
// an argument; the script never echoes it or writes it to a file.
var perforceUserDataHead = `#!/bin/bash
set -euo pipefail

SERVER_ID="{{ .ServerID }}"
DATA_MOUNT="{{ .DataMount }}"

# Stock Ubuntu has no AWS CLI, so install it before the SSM read.
` + userdata.AWSCLIInstallShell() + `
` + userdata.SecureParameterFetchShell("ADMIN_PASS", "{{ .AdminPasswordParameter }}", "{{ .Region }}", "Helix Core admin password") + `
`

// perforceUserDataBody follows the apt repo fragment (which ends by
// refreshing the apt index).
const perforceUserDataBody = `{{ if eq .Version "latest" -}}
apt-get install -y helix-p4d
{{- else -}}
# apt matches pkg=version against the full Debian version (for example
# 2025.2-3148719~jammy), so a bare release never matches. Resolve the pin to
# the newest repo build of that release (or the exact build for YYYY.N/BUILD)
# and fail closed when the repo no longer carries it (#699).
HELIX_PIN="$(echo '{{ .Version }}' | tr / -)"
HELIX_DEB_VERSION="$(apt-cache madison helix-p4d | awk -F'|' -v pin="$HELIX_PIN" '{v=$2; gsub(/ /, "", v); if (!found && (index(v, pin "-") == 1 || index(v, pin "~") == 1)) {print v; found = 1}}')"
if [ -z "$HELIX_DEB_VERSION" ]; then
  echo "ERROR: Helix Core {{ .Version }} is not in the Perforce apt repo (available: $(apt-cache madison helix-p4d | awk -F'|' '{gsub(/ /, "", $2); printf "%s ", $2}')). Set perforce.version to an available release, or \"latest\", and re-run fabrica perforce create." >&2
  exit 1
fi
apt-get install -y "helix-p4d=$HELIX_DEB_VERSION"
{{- end }}
echo "Installed helix-p4d $(dpkg-query -W -f='${Version}' helix-p4d)"

# Identify the data volume: the largest disk that is not the root disk.
# NVMe enumeration order is NOT deterministic across EC2 launches, so a
# hardcoded /dev/nvmeXnY can point at the root disk.
{{ if .DataDevice -}}
DATA_DEVICE="{{ .DataDevice }}"
echo "Using configured data device: $DATA_DEVICE"
{{- else }}
ROOT_SRC="$(findmnt -n -o SOURCE /)"
ROOT_DISK="$(lsblk -no PKNAME "$ROOT_SRC")"
DATA_DEVICE=""
DATA_SIZE=0
while read -r name size fstype; do
  [ "$name" = "$ROOT_DISK" ] && continue
  [ "$fstype" != "" ] && continue
  bytes=$(lsblk -bn -o SIZE "/dev/$name")
  if [ "$bytes" -gt "$DATA_SIZE" ]; then DATA_DEVICE="/dev/$name"; DATA_SIZE=$bytes; fi
done <<EOF
$(lsblk -dn -o NAME,SIZE,FSTYPE)
EOF
if [ -z "$DATA_DEVICE" ]; then
  echo "ERROR: no unformatted data volume found besides root ($ROOT_DISK)" >&2
  exit 1
fi
echo "Using data device: $DATA_DEVICE"
{{- end }}

mkfs.ext4 "$DATA_DEVICE"
mkdir -p "$DATA_MOUNT" /hxlogs /hxmetadata
mount "$DATA_DEVICE" "$DATA_MOUNT"
echo "$DATA_DEVICE $DATA_MOUNT ext4 defaults,nofail 0 2" >> /etc/fstab

# Perforce split Helix Core into the p4-server packages in 2026.1: the
# configure script dropped --super-passwd/-y for a positional service name +
# -P password, and instances are managed via p4dctl instead of a helix-p4d
# systemd unit. Detect the generation at runtime — the installed version can
# differ from the requested pin when the archive has dropped it.
CONFIGURE=/opt/perforce/sbin/configure-helix-p4d.sh
[ -x "$CONFIGURE" ] || CONFIGURE=/opt/perforce/sbin/configure-p4d.sh

if "$CONFIGURE" --help 2>&1 | grep -q -- '--super-passwd'; then
  "$CONFIGURE" -n "$SERVER_ID" -p 1666 -r "$DATA_MOUNT" \
    -u admin --super-passwd "$ADMIN_PASS" -y
else
  "$CONFIGURE" "$SERVER_ID" -n -p 1666 -r "$DATA_MOUNT" \
    -u admin -P "$ADMIN_PASS"
fi

# Start the server under whichever supervisor this generation ships.
if systemctl list-unit-files | grep -q '^helix-p4d'; then
  systemctl enable helix-p4d
  systemctl restart helix-p4d
else
  systemctl enable p4dctl
  p4dctl start "$SERVER_ID"
fi

`

// applyDefaults fills zero-value fields with module defaults.
// Does not validate required fields — use validate before calling.
func (cfg *UserDataConfig) applyDefaults() {
	if cfg.DataMount == "" {
		cfg.DataMount = "/hxdepots"
	}
	if cfg.ServerID == "" {
		cfg.ServerID = "fabrica-perforce"
	}
	// DataDevice intentionally left empty: the script auto-detects the
	// largest non-root volume, since NVMe enumeration order varies.
}

// validate checks required fields. Call after applyDefaults. The parameter
// name is written into the boot script, so only the per-create shape is
// accepted: a hand-edited state entry cannot inject shell into user data.
func (cfg *UserDataConfig) validate() error {
	if cfg.Region == "" {
		return fmt.Errorf("Region must not be empty: cloud-init reads the Helix Core admin password from SSM in that region")
	}
	if !AdminPasswordScope.IsName(cfg.AdminPasswordParameter) {
		return fmt.Errorf("AdminPasswordParameter %q is not a per-create %s/<nonce>/admin-password name; re-run 'fabrica perforce create' to mint one", cfg.AdminPasswordParameter, AdminPasswordScope.Prefix)
	}
	return nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect the script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(cfg.applyDefaults, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.Render(cfg)
}

// Generate renders the cloud-init user data script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(cfg.applyDefaults, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.RenderBase64(cfg)
}
