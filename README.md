# Fabrica

Game studio infrastructure as code for AWS.

Fabrica provisions the foundational systems a game development team needs to operate at scale — Perforce Helix Core repositories, Horde build farms, CI/CD pipelines, and cost visibility — from a single YAML configuration file.

It sits beside [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates the builds, Fabrica gives them somewhere to run.

Single binary. Zero external dependencies. Written in Go.

## Why Fabrica

Game studios aren't web apps. You need Perforce for terabyte asset histories, Horde for distributed shader compilation across dozens of machines, and a reliable way to stand all of it up without stitching together Terraform modules, bash scripts, and AWS console clicks. Fabrica handles the full lifecycle — provision, check status, tear down — with cost estimates before anything touches your account and DynamoDB-backed state so two engineers can't clobber each other's runs.

## Current Status

**Phase 0 complete. Perforce and Horde modules are implemented.**

| Module | Commands | Status |
|--------|----------|--------|
| `setup` / `doctor` | Foundation | Complete (setup is manual — see below) |
| `perforce` | `create`, `status`, `destroy` | Complete |
| `horde` | `create`, `status`, `submit` | Complete |
| `ci` | `setup`, `trigger`, `status`, `logs` | Planned |
| `deploy` | `setup`, `promote`, `status`, `destroy` | Planned |
| `workstation` | `create`, `list`, `stop`, `terminate` | Planned |
| `cost` | `report`, `forecast`, `alerts` | Planned |

> **Note:** `fabrica setup` does not yet create AWS resources. The S3 state bucket and DynamoDB lock table must be created manually before using any Fabrica commands. Run `fabrica setup --dry-run` to see the expected resource names, then create them yourself.

## Requirements

- Go 1.21+
- AWS credentials with permissions to create EC2 instances, security groups, S3 buckets, and DynamoDB tables
- IAM permission for `sts:GetCallerIdentity`

## Building

```bash
git clone https://github.com/jpvelasco/fabrica.git
cd fabrica
go build -o fabrica .
```

## Quick Start

```bash
# 1. Build the binary
go build -o fabrica .

# 2. Copy and edit the config
cp fabrica.example.yaml fabrica.yaml
# Set your region and (optionally) account_id

# 3. Create the state backend manually
#    Run --dry-run to get the exact resource names:
fabrica setup --dry-run
#    Then create the S3 bucket and DynamoDB table in AWS before proceeding.

# 4. Verify your environment is ready
fabrica doctor

# 5. Provision a Perforce Helix Core server
fabrica perforce create

# 6. Check when it's ready (probes port 1666)
fabrica perforce status

# 7. Provision an Unreal Horde build coordinator
#    IMPORTANT: You must supply a Horde AMI first — see docs/horde-ami.md
fabrica horde create

# 8. Submit a BuildGraph job
fabrica horde submit --buildgraph path/to/BuildGraph.xml --target "Compile UnrealGame Win64"
```

## Commands

### Foundation

#### `fabrica doctor`

Checks your environment: Go version, AWS credentials, region, S3 state bucket, DynamoDB lock table.

#### `fabrica setup`

> **Not yet functional for resource creation.** Shows a planning preview (`--dry-run`) and a cost estimate, but does not create any AWS resources. You must create the S3 bucket and DynamoDB table manually.

Run `fabrica setup --dry-run` to see expected resource names and estimated monthly cost (~$0.15).

#### `fabrica config show`

Displays the current configuration as clean YAML, including resolved resource names.

### Perforce

#### `fabrica perforce create`

Provisions a Perforce Helix Core server: creates an EC2 security group (port 1666) and launches an EC2 instance. Generates credentials to `.fabrica/perforce-credentials.yaml` (mode 0600). Writes state incrementally so partial failures are recoverable.

#### `fabrica perforce status`

Reads live state from AWS and TCP-probes port 1666. Transitions the module state from `provisioning` → `ready` once the server is reachable. Supports `--json` output.

#### `fabrica perforce destroy`

Terminates the EC2 instance and deletes the security group in reverse order. Idempotent — already-terminated instances are skipped.

### Horde

> **AMI requirement:** `fabrica horde create` is AMI-first. Your AMI must already contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica does not build or publish this AMI — it only configures and starts services via cloud-init. See [docs/horde-ami.md](docs/horde-ami.md) for what the AMI must contain.

#### `fabrica horde create`

Provisions an Unreal Horde build coordinator on an `m7i.xlarge` instance using your pre-baked AMI. Security group allows ports 5000 (HTTP), 5002 (gRPC), and inbound traffic from `10.0.0.0/8`. Generates MongoDB credentials to `.fabrica/horde-credentials.yaml` (mode 0600).

#### `fabrica horde status`

Reads live state and TCP-probes port 5000. Reports the Horde web UI URL and gRPC endpoint. `--json` emits `hordeUrl` and `hordeGrpc` fields.

#### `fabrica horde submit`

Parses a BuildGraph XML file and POSTs the job to the Horde REST API via the coordinator's private IP. Options:

```
--buildgraph   Path to BuildGraph XML file (required)
--target       Build target to run (required)
--wait         Poll until the job completes
```

Requires VPN or same-VPC access; no public IP is assigned in V1.

### Other

#### `fabrica version`

Prints version, commit hash, Go toolchain version, and platform.

## Configuration

```yaml
# fabrica.yaml
aws:
  account_id: "123456789012"   # auto-detected on first setup
  region: us-west-2

perforce:
  instance_type: c5.2xlarge
  ami_id: ami-xxxxxxxxxxxxxxxxx
  volume_size_gb: 500

horde:
  instance_type: m7i.xlarge
  ami_id: ami-xxxxxxxxxxxxxxxxx   # must contain MongoDB 7, Redis 6.2, Horde binary
```

## Architecture

```
cmd/* → internal/{config, state, cost, tags, prompt, cloud}
                                                    ↓
                                        internal/cloud/aws
```

`internal/*` packages are SDK-free pure plan layers. The `cmd/<module>` layer calls them, then executes via the AWS provider. See [AGENTS.md](AGENTS.md) for the full architecture and contribution guide.

## Contributing / Development

```bash
# Run tests (Windows)
go test ./...

# Run tests with race detector (Linux/macOS)
go test -race -coverprofile=coverage.out -covermode=atomic ./...

# Lint
golangci-lint run ./...

# Activate git hooks (once per clone)
git config core.hooksPath .githooks
```

Pull requests go to `main`. Each PR should pass CI (lint + build + test on ubuntu/windows/macos) before merging. New commands follow the `cmd/perforce/` and `internal/perforce/` templates — see [AGENTS.md](AGENTS.md) for the full pattern.

## License

See [LICENSE](LICENSE) for details.
