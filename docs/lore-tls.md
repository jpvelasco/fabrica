# Lore TLS

**Status:** Wired on create. Config drives cloud-init; Fabrica does not provision certificates.

## Config

```yaml
lore:
  amiId: ami-lore-123
  tls:
    enabled: true
    certPath: /etc/loreserver/certs/server.crt
    keyPath:  /etc/loreserver/certs/server.key
```

When `tls.enabled` is `true`:

1. `certPath` and `keyPath` must be absolute paths on the AMI.
2. `fabrica lore create` writes those paths into `/etc/loreserver/local.toml` as `[server] tls_cert` / `tls_key`.
3. Cloud-init fails the instance if either file is missing at boot.

When `tls.enabled` is `false` (default), no TLS block is written and loreserver starts without those paths.

## Operator requirements

- Bake the certificate and key into the AMI at the configured paths. Fabrica does not generate, push, or rotate them.
- Clients must trust the AMI cert (or use the client's skip-verify option for lab use).
- Restrict `lore.allowedCidr`. TLS is defense in depth, not a substitute for the security group.
- Local-store create attaches a slim SSM instance profile so operators can reach the instance in-band. S3-store still uses the fuller store+SSM role. Create does not require SSM endpoints to succeed.

## Still deferred

| Feature | Notes |
| --- | --- |
| Certificate provisioning | Operator-managed via AMI rebuild or SSM |
| ACM integration | Not wired |
| Certificate rotation | Operator-managed |
| Client certificate auth / mTLS | Not supported |
| JWT authentication | Operator-configured on the Lore side |
| HTTPS health probe | Status remains `GET http://:41339/health_check` |

## Architecture

All Lore client traffic stays on private IPs. The health endpoint on `:41339` remains HTTP.
