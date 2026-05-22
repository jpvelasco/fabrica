# fabrica perforce

Manages a single-server Perforce Helix Core installation on AWS.

## Lifecycle

```
fabrica perforce create    # Provision security group + EC2 instance
fabrica perforce status    # Show health and P4PORT
fabrica perforce destroy   # Permanently delete all resources
```

## What gets created

| Resource | Name | Notes |
|---|---|---|
| EC2 Security Group | `fabrica-perforce-sg` | TCP 1666 inbound open (restrict for production) |
| EC2 Instance | `fabrica-perforce` | Helix Core installed via user-data; gp3 EBS data volume |

## State

Module state is stored in `.fabrica/state.json` (local cache) and mirrored to S3 after `fabrica setup`. State is written after each resource creation or deletion, so partial failures leave a recoverable record.

## Credentials

`fabrica perforce create` generates a random admin password and writes it to `.fabrica/perforce-credentials.yaml` (mode 0600). **Rotate this password after first login.**

## Flags

Global flags that apply to all perforce subcommands (set on the root command):

| Flag | Short | Default | Effect |
|---|---|---|---|
| `--dry-run` | | false | Show plan without making AWS calls |
| `--yes` | `-y` | false | Skip interactive confirmation |
| `--json` | | false | Emit machine-readable JSON instead of text |

Command-specific flags for `create`:

| Flag | Default | Effect |
|---|---|---|
| `--instance-type` | `m5.xlarge` | EC2 instance type |
| `--version` | `2024.2` | Helix Core version: `latest`, `YYYY.N`, or `YYYY.N/BUILD` |
| `--volume-size` | `500` | EBS data volume size in GiB |

## Architecture notes

- All AWS calls go through `cloud.ResourceClient` (Cloud Control API) — no Terraform, no CDK.
- `internal/perforce` is a pure library: plan building, resource desired-state generation, cost estimators, and user-data templating. No AWS SDK imports.
- `cmd/perforce/{create,status,destroy}` are the Cobra command wrappers. Each command struct holds injectable seams (`readState`, `writeState`, `createResource` / `deleteResource` / `getResource`) for white-box testing without AWS.
