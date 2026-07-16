# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it privately rather
than opening a public issue.

**Preferred:** open a
[GitHub Security Advisory](https://github.com/jpvelasco/fabrica/security/advisories/new).

**Include:**

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

We'll acknowledge receipt within 48 hours and aim to provide a fix timeline
within 7 days.

## Supported Versions

We provide security updates for the latest release only.

## Scope notes

- Do not report issues that require access to your own AWS account secrets or
  local `fabrica.yaml` / `.fabrica/*` credentials unless you can share a
  minimal, redacted reproduction.
- Credential files written by Fabrica (under `.fabrica/`) are mode `0600` by
  design; treat them as secrets and never commit them.
