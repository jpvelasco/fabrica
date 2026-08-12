# Lore TLS — Design Note

**Status:** Hooks only (V1). Config fields exist; cloud-init wiring deferred to V2.

## What Exists Today

The `LoreTLSConfig` struct is defined in `internal/config/config.go` and is
readable from `fabrica.yaml`:

```yaml
lore:
  tls:
    enabled: false
    certPath: /etc/loreserver/certs/server.crt
    keyPath:  /etc/loreserver/certs/server.key
```

**These fields are not yet wired into cloud-init.** The `UserDataConfig` struct
in `internal/lore/userdata.go` has `TLSEnabled`, `CertPath`, and `KeyPath`
fields, but the cloud-init template does not reference them and `lore create`
does not pass them through. Setting `tls.enabled: true` is currently a no-op —
the fields exist as placeholders for V2 implementation.

## What Will Be Enabled (V2)

When TLS wiring is implemented, the cloud-init script will:

1. Verify the certificate and key files exist at the specified paths
2. Start loreserver with TLS flags pointing to those paths
3. The AMI must already contain the certificate and key files at the specified paths

## What Is Deferred (V2)

The following are **not** in scope for the current implementation:

| Feature | Status | Notes |
|---------|--------|-------|
| Certificate provisioning | Deferred | Fabrica does not generate or manage certificates |
| ACM integration | Deferred | No AWS ACM integration in V1 |
| Certificate rotation | Deferred | Operator-managed via AMI rebuild or SSM |
| Client certificate auth | Deferred | mTLS not supported in V1 |
| JWT authentication | Deferred | Lore's JWT auth is operator-configured |
| HTTPS health probe | Deferred | Status probe remains HTTP on 41339 in V1 |
| Automatic cert generation | Deferred | Self-signed certs must be baked into the AMI |

## Architecture Constraints

- **AMI-first:** Certificates must be pre-baked into the AMI at the paths
  specified in `certPath` and `keyPath`. Fabrica does not push certificates
  to the instance at boot time.
- **Private IP only:** Lore instances run in private subnets. TLS is primarily
  useful for protecting data in transit between VPC components (workstations,
  Horde agents) and the Lore server.
- **No public exposure:** The security group controls network access via
  `lore.allowedCidr`. TLS is a defense-in-depth measure, not a replacement
  for network segmentation.

## Security Model

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│  Workstation │────────>│   Lore      │<────────│  Horde Agent │
│  (gRPC/QUIC) │  TLS    │  Server     │  TLS    │  (gRPC/QUIC) │
│  Private IP  │◄──────── │  :41337    │────────>│  Private IP  │
└─────────────┘         └─────────────┘         └─────────────┘
                            │
                       :41339 HTTP
                     (health probe —
                      unencrypted in V1)
```

All Lore traffic flows through private IPs within the VPC. The health check
endpoint (`:41339`) remains unencrypted in V1 — this is acceptable as it
exposes only a health status string, not credentials or game data.

## Usage (V2 — not yet active)

The following config is accepted by the parser but has no effect until V2
wires TLS into cloud-init:

```yaml
# fabrica.yaml (accepted but no-op until V2)
lore:
  amiId: ami-lore-123
  instanceType: m7i.xlarge
  volumeSize: 500
  tls:
    enabled: true
    certPath: /etc/loreserver/certs/server.crt
    keyPath:  /etc/loreserver/certs/server.key
```

When V2 ships, the AMI must contain the certificate files at the specified
paths before Fabrica provisions the instance.

## Future Work

- **V2:** Automated certificate provisioning via SSM or Secrets Manager
- **V2:** ACM certificate integration for managed rotation
- **V2:** HTTPS health probe (`GETs` on `:41339`)
- **V3:** mTLS for client authentication
- **V3:** JWT token configuration via Fabrica
