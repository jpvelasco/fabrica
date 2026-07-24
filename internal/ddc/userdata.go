package ddc

import (
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is input for the DDC (Jupiter) cloud-init script.
// Single home-region only — no remote peer list.
type UserDataConfig struct {
	StorePath    string
	Bucket       string
	Region       string
	Namespace    string
	PublicPort   int
	InternalPort int
	Backend      string
	// ScyllaContact is set only when Backend=scylla (private IP filled later or placeholder).
	// V1 cloud-init uses localhost discovery if empty and backend is scylla on co-located path;
	// when Scylla is a separate instance, setup injects a note for operator restart after status.
	ScyllaContact string
}

var userDataRenderer = userdata.New(template.Must(template.New("ddc-userdata").Option("missingkey=error").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-ddc-init.log) 2>&1

STORE="{{ .StorePath }}"
BUCKET="{{ .Bucket }}"
REGION="{{ .Region }}"
NS="{{ .Namespace }}"
PUBLIC_PORT="{{ .PublicPort }}"
INTERNAL_PORT="{{ .InternalPort }}"
BACKEND="{{ .Backend }}"
SCYLLA_CONTACT="{{ .ScyllaContact }}"

resolve_data_dev() {
  if [ -b /dev/sdf ]; then echo /dev/sdf; return 0; fi
  if [ -b /dev/xvdf ]; then echo /dev/xvdf; return 0; fi
  local root_src root_disk d
  root_src=$(findmnt -n -o SOURCE / 2>/dev/null || true)
  root_disk=$(echo "$root_src" | sed -E 's/p?[0-9]+$//')
  for d in /dev/nvme*n1; do
    [ -e "$d" ] && [ -b "$d" ] || continue
    if [ -n "$root_disk" ] && [ "$d" = "$root_disk" ]; then continue; fi
    echo "$d"; return 0
  done
  return 1
}

DATA_DEV=""
for i in $(seq 1 30); do
  if DATA_DEV=$(resolve_data_dev); then break; fi
  DATA_DEV=""
  if [ "$i" -eq 30 ]; then
    echo "ERROR: data volume not found"
    exit 1
  fi
  sleep 2
done
echo "Using data device: $DATA_DEV"
mkdir -p "$STORE"
if ! blkid "$DATA_DEV" >/dev/null 2>&1; then
  mkfs.ext4 -F "$DATA_DEV"
fi
if ! mountpoint -q "$STORE"; then
  mount "$DATA_DEV" "$STORE" || { sleep 2; mount "$DATA_DEV" "$STORE"; }
fi
grep -q "$STORE" /etc/fstab || echo "$DATA_DEV $STORE ext4 defaults,nofail 0 2" >> /etc/fstab

# Write a minimal Fabrica-managed config fragment. The AMI must provide the
# Unreal Cloud DDC (Jupiter) binary/service unit; this only points it at hybrid storage.
CONFIG_DIR=/etc/unreal-cloud-ddc
mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/fabrica.env" <<EOF
FABRICA_DDC_BUCKET=$BUCKET
FABRICA_DDC_REGION=$REGION
FABRICA_DDC_NAMESPACE=$NS
FABRICA_DDC_PUBLIC_PORT=$PUBLIC_PORT
FABRICA_DDC_INTERNAL_PORT=$INTERNAL_PORT
FABRICA_DDC_BACKEND=$BACKEND
FABRICA_DDC_STORE=$STORE
FABRICA_DDC_SCYLLA_CONTACT=$SCYLLA_CONTACT
EOF

# Single-region V1: no remote replication peer list.
systemctl enable unreal-cloud-ddc 2>/dev/null || true
systemctl restart unreal-cloud-ddc 2>/dev/null || systemctl start unreal-cloud-ddc 2>/dev/null || true
echo "Fabrica DDC cloud-init complete (backend=$BACKEND)"
`)))

// applyDefaults fills zero-value fields with module defaults.
func (cfg *UserDataConfig) applyDefaults() {
	if cfg.StorePath == "" {
		cfg.StorePath = DefaultStorePath
	}
}

// Generate returns base64-encoded user data for the DDC instance.
func Generate(cfg UserDataConfig) (string, error) {
	cfg.applyDefaults()
	return userDataRenderer.RenderBase64(cfg)
}

// GenerateRaw returns plain-text cloud-init for tests.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	cfg.applyDefaults()
	return userDataRenderer.Render(cfg)
}

// ScyllaUserDataConfig is cloud-init for the optional 1-node Scylla bootstrap host.
type ScyllaUserDataConfig struct {
	StorePath   string
	ClusterName string
}

var scyllaUserDataRenderer = userdata.New(template.Must(template.New("scylla-userdata").Option("missingkey=error").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-ddc-scylla-init.log) 2>&1
STORE="{{ .StorePath }}"
CLUSTER="{{ .ClusterName }}"

resolve_data_dev() {
  if [ -b /dev/sdf ]; then echo /dev/sdf; return 0; fi
  if [ -b /dev/xvdf ]; then echo /dev/xvdf; return 0; fi
  local root_src root_disk d
  root_src=$(findmnt -n -o SOURCE / 2>/dev/null || true)
  root_disk=$(echo "$root_src" | sed -E 's/p?[0-9]+$//')
  for d in /dev/nvme*n1; do
    [ -e "$d" ] && [ -b "$d" ] || continue
    if [ -n "$root_disk" ] && [ "$d" = "$root_disk" ]; then continue; fi
    echo "$d"; return 0
  done
  return 1
}
DATA_DEV=""
for i in $(seq 1 30); do
  if DATA_DEV=$(resolve_data_dev); then break; fi
  if [ "$i" -eq 30 ]; then echo "ERROR: data volume not found"; exit 1; fi
  sleep 2
done
mkdir -p "$STORE"
if ! blkid "$DATA_DEV" >/dev/null 2>&1; then mkfs.ext4 -F "$DATA_DEV"; fi
mountpoint -q "$STORE" || mount "$DATA_DEV" "$STORE"
# AMI must contain Scylla; enable service. V1 is single-node bootstrap only.
systemctl enable scylla-server 2>/dev/null || true
systemctl restart scylla-server 2>/dev/null || systemctl start scylla-server 2>/dev/null || true
echo "Fabrica DDC Scylla bootstrap complete cluster=$CLUSTER (NOT production HA)"
`)))

// applyDefaults fills zero-value fields with module defaults.
func (cfg *ScyllaUserDataConfig) applyDefaults() {
	if cfg.StorePath == "" {
		cfg.StorePath = "/var/lib/scylla"
	}
	if cfg.ClusterName == "" {
		cfg.ClusterName = "fabrica-ddc"
	}
}

// GenerateScylla returns base64 user data for the Scylla bootstrap instance.
func GenerateScylla(cfg ScyllaUserDataConfig) (string, error) {
	cfg.applyDefaults()
	return scyllaUserDataRenderer.RenderBase64(cfg)
}

// GenerateScyllaRaw returns plain-text Scylla cloud-init for tests.
func GenerateScyllaRaw(cfg ScyllaUserDataConfig) (string, error) {
	cfg.applyDefaults()
	return scyllaUserDataRenderer.Render(cfg)
}
