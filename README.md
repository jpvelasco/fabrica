# Fabrica

[![codecov](https://codecov.io/gh/jpvelasco/fabrica/graph/badge.svg?token=YOUR_GRAPH_TOKEN)](https://codecov.io/gh/jpvelasco/fabrica)

Game studio infrastructure as code for AWS.

Fabrica provisions the foundational systems a game development team needs to operate at scale — Perforce Helix Core repositories, Horde build farms, CI/CD pipelines, and cost visibility — from a single YAML configuration file.

It sits beside [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates the builds, Fabrica gives them somewhere to run.

Single binary. Zero external dependencies. Written in Go.

## Why Fabrica

Game studios aren't web apps. You need Perforce for terabyte asset histories, Horde for distributed shader compilation across dozens of machines, and a reliable way to stand all of it up without stitching together Terraform modules, bash scripts, and AWS console clicks. Fabrica handles the full lifecycle — provision, check status, tear down — with cost estimates before anything touches your account and DynamoDB-backed state so two engineers can't clobber each other's runs.

## Current Status

**Phase 0 & Phase 1 complete.** All provisioning modules (Perforce, Horde,
Workstation), CI, Deploy, and Cost ship today, along with full-stack
`destroy --all` teardown, offline cost visibility, and a CLI end-to-end test
suite. Release machinery (GoReleaser + npm shim) is wired but dormant — no
release is cut until a `v*` tag is pushed.
See [ROADMAP.md](ROADMAP.md) for phases, the Praetorium vision, and what's next.

| Module | Commands | Status |
|--------|----------|--------|
| `setup` / `doctor` / `status` | Foundation | Complete |
| `perforce` | `create`, `status`, `destroy` | Complete |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | Complete |
| `lore` | `create`, `status`, `destroy` | Complete |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | Complete |
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | Complete |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | Complete |
| `cost` | `report`, `forecast`, `alerts` | Complete |
| `destroy --all` | full-stack teardown | Complete |

## Requirements

- Go 1.25.12+
- AWS credentials with permissions to create EC2 instances, security groups, S3 buckets, and DynamoDB tables
- IAM permission for `sts:GetCallerIdentity`

## Install

Fabrica ships as a single Go binary. Two ways to get it:

```bash
# Via npm (downloads the matching prebuilt binary for your platform):
npm install -g fabrica-cli
# …or run without installing:
npx fabrica-cli --help

# Or via the Go toolchain:
go install github.com/jpvelasco/fabrica@latest
```

Prebuilt binaries for linux/macOS/windows (amd64) and linux/macOS (arm64) are
attached to each [GitHub Release](https://github.com/jpvelasco/fabrica/releases).

## Building

```bash
git clone https://github.com/jpvelasco/fabrica.git
cd fabrica
go build -o fabrica .
```

## Getting Started

The ideal first five commands, in order:

```bash
# 1. Build the binary
go build -o fabrica .

# 2. Copy and edit the config — set your AWS region (and optionally accountId)
cp fabrica.example.yaml fabrica.yaml

# 3. Create the state backend (S3 bucket + DynamoDB lock table).
#    Preview first, then create — setup is idempotent and asks before it writes:
fabrica setup --dry-run      # shows the plan + monthly cost estimate, no changes
fabrica setup                # creates the backend (prompts y/N; use --yes in CI)

# 4. Confirm everything is healthy
fabrica doctor               # checks credentials, region, bucket, lock table
fabrica status               # one-line health overview across all modules

# 5. Provision your first module
fabrica perforce create
fabrica perforce status      # watch it become ready (probes port 1666)
```

Then grow the studio from there:

```bash
# Unreal Horde build coordinator (supply a Horde AMI first — see docs/horde-ami.md)
fabrica horde create
fabrica horde submit --buildgraph path/to/BuildGraph.xml --target "Compile UnrealGame Win64"

# Lore VCS server (parallel alternative to Perforce — see docs/lore-ami.md)
fabrica lore create
fabrica lore status -w

# Distributed DDC (Unreal Cloud DDC — single home-region; see docs/ddc-ami.md)
fabrica ddc setup
fabrica ddc status --probe

# A cloud workstation
fabrica workstation create

# CI: a CodeBuild project that orchestrates Horde BuildGraph jobs
fabrica ci setup
fabrica ci trigger BuildGraph.xml --wait

# Deploy: roll a built server out to a GameLift fleet (blue/green), then check it
fabrica deploy setup
fabrica deploy promote v1.0.0
fabrica deploy status

# Re-run any time for an aggregate view (add --probe from a VPN to test reachability)
fabrica status
```

### End-to-end: from source to a deployed build

The CI and deploy modules form one pipeline. `ci trigger` runs a BuildGraph job
on Horde and its packaged server lands in S3 at the convention path
`s3://<deploy.buildBucket>/builds/<version>/server.zip`; `deploy promote <version>`
then registers that build and rolls it out to a fresh GameLift fleet (blue/green),
flipping the alias only once the fleet is `ACTIVE`:

```bash
fabrica setup --yes                       # 1. state backend (once)
fabrica horde create && fabrica ci setup  # 2. build farm + CI orchestration
fabrica ci trigger BuildGraph.xml --wait  # 3. build → server.zip in S3
fabrica deploy setup                      # 4. GameLift alias + role (once)
fabrica deploy promote v1.0.0             # 5. new fleet from that build, alias flip
fabrica deploy status                     # 6. confirm the alias target + fleet health
fabrica deploy rollback                   # instant alias flip back if v1.0.0 misbehaves
```

Set `deploy.buildBucket` in `fabrica.yaml` so promote knows where CI's output
lives. Tear the whole studio down with `fabrica destroy --all` when you're done.

## Commands

### Foundation

#### `fabrica doctor`

Checks your environment: Go version, AWS credentials, region, S3 state bucket, DynamoDB lock table.

#### `fabrica setup`

Creates the state backend for this account: the S3 bucket (versioning, encryption, and public-access block) and the DynamoDB lock table. Idempotent — re-running reconciles configuration and leaves existing resources in place.

- `fabrica setup --dry-run` — preview resource names and estimated monthly cost (~$0.15), no changes.
- `fabrica setup` — create the backend after a y/N confirmation.
- `fabrica setup --yes` — skip the prompt (CI / automation).

#### `fabrica status`

Read-only aggregate overview of every provisioned module plus the state backend: a one-line health summary, per-module status with `[OK]`/`[WARN]` indicators and resource counts, and context-aware next steps. `--json` for scripts; `--probe` adds TCP readiness checks (requires VPN / in-VPC reachability).

#### `fabrica config show`

Displays the current configuration as clean YAML, including resolved resource names.

### Perforce

#### `fabrica perforce create`

Provisions a Perforce Helix Core server: creates an EC2 security group (port 1666) and launches an EC2 instance. Generates credentials to `.fabrica/perforce-credentials.yaml` (mode 0600). Writes state incrementally so partial failures are recoverable.

#### `fabrica perforce status`

Reads live state from AWS and TCP-probes port 1666. Transitions the module state from `provisioning` → `ready` once the server is reachable. Supports `--json` output.

#### `fabrica perforce destroy`

Terminates the EC2 instance, IAM instance profile/role, and security group in reverse order. Idempotent — already-terminated instances are skipped. The data volume is **retained** (`DeleteOnTermination=false`) so local backups survive as an orphan EBS volume; S3 exports are never deleted by destroy.

#### `fabrica perforce backup`

Creates a consistent Helix Core backup on the instance EBS volume (under `/hxdepots/fabrica-backups` by default) via SSM Run Command. Optional S3 export when `perforce.backup.s3Export` is set. Checkpoint briefly quiesces the server — prefer a quiet window. Requires a ready module and an SSM-managed instance profile (attached at `perforce create`).

```
--name           Optional short name appended to the backup id
--description    Stored in backup metadata
--no-s3          Skip S3 export even if configured
```

#### `fabrica perforce backup list`

Lists backups on the server (reads `metadata.json` over SSM). Supports `--json`.

#### `fabrica perforce backup delete`

Deletes a backup by id from the EBS volume (and S3 when metadata has `s3Uri`).

#### `fabrica perforce restore`

Restores Helix Core from a backup id: stops `helix-p4d`, restores checkpoint/journal artifacts, restarts. Requires `--force` when the server is ready (serving clients). Confirmation phrase: `restore perforce <account-id>`.

### Horde

> **AMI requirement:** `fabrica horde create` is AMI-first. Your AMI must already contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica does not build or publish this AMI — it only configures and starts services via cloud-init. See [docs/horde-ami.md](docs/horde-ami.md) for what the AMI must contain.

#### `fabrica horde create`

Provisions an Unreal Horde build coordinator on an `m7i.2xlarge` instance using your pre-baked AMI. Security group allows ports 5000 (HTTP), 5002 (gRPC), and inbound traffic from `10.0.0.0/8`. Generates MongoDB credentials to `.fabrica/horde-credentials.yaml` (mode 0600).

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

#### `fabrica horde destroy`

Permanently deletes the Horde coordinator and its AWS resources in reverse-creation order (EC2 instance, then security group). State is updated after each deletion, so a partial failure leaves a recoverable record and re-running skips resources already gone. Typed-phrase confirmation; `--yes` to skip, `--dry-run` to preview.

#### `fabrica horde ami build`

Generates the files needed to build a Horde AMI. Produces an EC2 Image Builder component (`component.yaml`) and recipe (`image-builder-recipe.json`) by default, an optional Packer HCL template (`--include-packer`), and a `build-guide.md` with end-to-end instructions. No AWS calls are made — all output is written to a local directory.

Two install methods are supported:

```
--install docker   (default) Epic's official docker compose stack
--install native   .NET 8 + MongoDB 7 + Redis installed directly from apt
```

Key flags: `--horde-version`, `--base-image`, `--region`, `--output-dir`, `--include-packer`, `--dry-run`.

### Lore

> **AMI requirement:** `fabrica lore create` is AMI-first. Your AMI must already contain the `loreserver` binary (and optional systemd unit). Fabrica only mounts the EBS store, writes local store config, and starts the service. See [docs/lore-ami.md](docs/lore-ami.md). Lore is a **parallel** VCS option alongside Perforce — both modules can coexist.

#### `fabrica lore create`

Provisions an Epic Lore (`loreserver`) server: security group opens TCP 41337 (gRPC), UDP 41337 (QUIC), and TCP 41339 (HTTP health); EC2 instance uses your pre-baked AMI with a gp3 data volume for local store. Connection notes go to `.fabrica/lore-credentials.yaml` (mode 0600). V1 uses local/EBS storage, self-signed TLS, and no JWT.

#### `fabrica lore status`

Reads live state and probes `GET /health_check` on port 41339. Transitions `provisioning` → `ready` when healthy. `--json` emits `loreUrl` and `loreGrpc`. Supports `--wait` / `-w`.

#### `fabrica lore destroy`

Terminates the EC2 instance and deletes the security group in reverse order. Idempotent. Typed-phrase confirmation; `--yes` to skip, `--dry-run` to preview.

### DDC

> **AMI requirement:** `fabrica ddc setup` is AMI-first. Your AMI must already contain Unreal Cloud DDC (Jupiter). Fabrica mounts the hot EBS volume, writes hybrid-storage config, and starts the service. See [docs/ddc-ami.md](docs/ddc-ami.md).
>
> **V1 scope:** single home-region EC2 only (co-located coordinator + edge roles). **No `region add` (or any multi-region command) in V1** — deferred to a later milestone. Default backend is `zen`. Scylla is an optional single-node bootstrap path only (not production HA).

#### `fabrica ddc setup`

Provisions IAM role + instance profile, S3 blob bucket, security group, optional Scylla bootstrap EC2, and the DDC EC2 instance. Writes `.fabrica/ddc-endpoints.yaml`. Cost estimate and dry-run supported. Idempotent if already provisioned.

```
--backend    zen (default) or scylla (1-node bootstrap only — not HA)
```

#### `fabrica ddc status`

Reads live state and probes `GET /health/ready` on the public API port. Transitions `provisioning` → `ready` when healthy. Supports `--wait` / `-w` and `--json`.

#### `fabrica ddc destroy`

Deletes DDC resources in reverse order (instances → bucket → IAM → SG). Non-empty S3 buckets are not force-deleted. Typed-phrase confirmation; `--yes` to skip, `--dry-run` to preview.

### Workstation

> **AMI requirement:** `fabrica workstation create` is AMI-first. Your AMI must already have NICE DCV installed. Fabrica only configures and starts the DCV session via cloud-init. Port 8443 (NICE DCV HTTPS) is opened inbound; restrict `workstation.allowedCidr` in `fabrica.yaml` for production.

#### `fabrica workstation create`

Provisions a NICE DCV cloud workstation: creates an EC2 security group (port 8443) and launches an EC2 instance. Generates a DCV session password to `.fabrica/workstation-credentials.yaml` (mode 0600).

Key flags:

```
--instance-type    EC2 instance type (default: g4dn.xlarge)
--volume-size      EBS root volume size in GiB (default: 100)
--template         Preset: "artist" (g6.xlarge, 200 GiB) or "programmer" (c7i.xlarge, 100 GiB)
--mount-perforce   Install Perforce CLI and write ~/.p4config from local Fabrica state
```

When `--mount-perforce` is set, `create` reads the Perforce server's private IP from local state (requires `fabrica perforce create` to have run first) and writes `~/.p4config` on the workstation with `P4PORT` set. The developer still runs `p4 sync` manually.

#### `fabrica workstation list`

Displays provisioned workstation status and resource IDs. Supports `--json`.

#### `fabrica workstation stop`

Stops the EC2 instance to pause compute billing. Data and configuration are preserved. Supports `--dry-run`, `--yes`, `--json`.

#### `fabrica workstation start`

Starts a previously stopped workstation. Supports `--dry-run`, `--yes`, `--json`.

#### `fabrica workstation terminate`

Permanently terminates the workstation EC2 instance and security group. Deletes resources in reverse-creation order. Idempotent — already-terminated instances are skipped. Supports `--dry-run`, `--yes`, `--json`.

### CI

> **Orchestration over Horde:** `fabrica ci` provisions a CodeBuild project that orchestrates Horde BuildGraph jobs. CodeBuild is the conductor; Horde stays the executor. The IAM service role is created via Cloud Control; the CodeBuild project via the AWS SDK (Cloud Control does not support CodeBuild project creation).

#### `fabrica ci setup`

Provisions the CI infrastructure for this account: an IAM service role and a CodeBuild project. Idempotent — existing resources are detected and left in place. Shows a plan + monthly cost estimate, then prompts before creating (use `--yes` to skip, `--dry-run` to preview).

#### `fabrica ci trigger <buildgraph.xml>`

Starts a build run. Parses the BuildGraph XML for the job name and target, resolves the Horde coordinator's address from local state, and starts the CodeBuild project with those values as environment overrides; the build submits the job to Horde. Requires `fabrica ci setup` and a provisioned, reachable Horde coordinator. Use `--wait` to poll until the build reaches a terminal state.

#### `fabrica ci status`

Shows the CI infrastructure (CodeBuild project + IAM role) from local state, with `[OK]`/`[WARN]` indicators and a one-line summary. Pass `--build <id>` to also query live build status; `--json` for machine-readable output. Read-only.

#### `fabrica ci logs <build-id>`

Fetches the CloudWatch log output for a specific build.

#### `fabrica ci destroy`

Tears down the CI infrastructure: deletes the CodeBuild project (via the AWS SDK), then the IAM service role (via Cloud Control). A missing project is not an error. Typed-phrase confirmation before any deletion; `--yes` to skip, `--dry-run` to preview.

**Example pipeline:**

```bash
# One-time: provision the CI infrastructure
fabrica ci setup

# Trigger a build for a BuildGraph script and watch it run
fabrica ci trigger BuildGraph.xml --wait

# Or fire-and-forget, then check status / logs by build ID
fabrica ci trigger BuildGraph.xml
fabrica ci status --build <build-id>
fabrica ci logs <build-id>
```

### Deploy

> **Orchestration over GameLift:** `fabrica deploy` rolls CI/Horde-produced server builds out to GameLift managed-EC2 fleets using alias-flip blue/green. Fabrica owns the build-to-deploy path; live runtime fleet operations (scaling, matchmaking, sessions) are left to Classis. GameLift Build/Fleet/Alias resources are created via Cloud Control; the 20–40 min fleet activation is tracked through an SDK auxiliary interface so you see live phase progress and real failure events.

#### `fabrica deploy setup`

Provisions the deploy infrastructure: an IAM role GameLift assumes to read builds from S3, and a GameLift alias used for blue/green promotion. Idempotent — existing resources are detected and left in place. Shows a plan + monthly cost estimate, then prompts before creating (`--yes` to skip, `--dry-run` to preview). Requires `deploy.buildBucket` in `fabrica.yaml`.

#### `fabrica deploy promote <build-version>`

Registers a packaged server build from S3 as a GameLift build, creates a new fleet for it, waits until the fleet is `ACTIVE` (printing phase transitions), then flips the alias to the new fleet. The previously-active fleet is retained for rollback. By default the build is read from `s3://<deploy.buildBucket>/builds/<version>/server.zip` (override with `--s3-bucket`/`--s3-key`). Use `--no-wait` to start fleet creation without blocking (skips the alias flip). On fleet `ERROR` or timeout the alias is left untouched and the failure events are shown.

#### `fabrica deploy rollback`

Flips the alias back to the most-recent retained ("superseded") fleet — an instant blue/green rollback with no re-provisioning. Verifies the target fleet is still `ACTIVE` first, shows current → target, and prompts before flipping (`--yes` to skip).

#### `fabrica deploy status`

Read-only overview: the alias, the active fleet, and any retained rollback candidates, each with live GameLift fleet status (`[OK]`/`[....]`/`[FAIL]` indicators) and a one-line summary. `--json` for machine-readable output.

#### `fabrica deploy destroy`

Tears down deploy resources. By default deletes the fleets and builds but **preserves** the alias and IAM role (game backends reference the alias, which is meant to outlive individual deployments). Pass `--all` to remove the alias and role too. Typed-phrase confirmation; `--dry-run` to preview.

**Example pipeline:**

```bash
# One-time: provision the deploy infrastructure (IAM role + alias)
fabrica deploy setup

# Roll a build out to a new fleet (blue/green: waits for ACTIVE, flips alias)
fabrica deploy promote v1.2.3

# Check what's live and what you could roll back to
fabrica deploy status

# A bad build? Flip back to the previous fleet instantly
fabrica deploy rollback

# Tear down fleets/builds (keeps the alias + role for the next promote)
fabrica deploy destroy
```

### Cost

> **Offline cost visibility:** `fabrica cost` estimates monthly cost for the modules present in local state, preferring the deployed shape recorded in state (instance type, volume/fleet size) and falling back to your current `fabrica.yaml` for anything not recorded. Fully offline — no AWS Cost Explorer calls, no billing API. Run `<module> status` to reconcile if state and reality have drifted.

#### `fabrica cost report`

Shows the estimated monthly cost broken down by provisioned module and resource, with a grand total and confidence level. Reads the deployed shape from local state where recorded, falling back to `fabrica.yaml` for cost inputs not yet in state. `--json` for machine-readable output.

#### `fabrica cost forecast`

Projects the current monthly estimate over a time horizon: daily burn rate, total over the horizon, and annualized cost. `--days <n>` sets the horizon (default 30). `--json` for machine-readable output.

#### `fabrica cost alerts`

Manages local budget thresholds (written to `fabrica.yaml` — no AWS Budgets resources are created) and checks the current estimate against them:

- `fabrica cost alerts list` — show configured thresholds.
- `fabrica cost alerts set <scope> <monthly> [--warn-pct N]` — upsert a threshold (`scope` is `total` or a module name; `--warn-pct` defaults to 80). Honors `--dry-run`.
- `fabrica cost alerts check` — evaluate the current estimate against thresholds and report OK/WARN/OVER. Informational (exit code stays 0). `--json` for machine-readable output.

### Other

#### `fabrica destroy --all`

Full-stack teardown: destroys every provisioned module in reverse dependency order (deploy → ci → workstation → horde → lore → perforce), then the state backend — but only if every module succeeded (a module failure preserves the backend so orphaned resources stay tracked for retry). One aggregate typed-phrase confirmation; `--yes` to skip, `--dry-run` to preview the full plan. Plain `fabrica destroy` (no `--all`) just prints usage.

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
  instance_type: m7i.2xlarge
  ami_id: ami-xxxxxxxxxxxxxxxxx   # must contain MongoDB 7, Redis 6.2, Horde binary

lore:
  amiId: ami-xxxxxxxxxxxxxxxxx    # must contain loreserver (see docs/lore-ami.md)
  instanceType: m5.xlarge
  volumeSize: 500
  allowedCidr: 10.0.0.0/8
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
