# fabrica-cli

**Game studio infrastructure as code for AWS** — one binary and one YAML file to
provision Perforce (or Lore), Unreal Horde, Distributed DDC, CI, GameLift deploy,
and cloud workstations.

Sister tool to [Ludus](https://www.npmjs.com/package/ludus-cli): **Ludus** builds
and cooks Unreal dedicated servers; **Fabrica** gives them somewhere to run.
Single Go binary under the hood — no Terraform, no Pulumi, no extra CLIs. AWS
Cloud Control API, plan + cost estimates before you write, DynamoDB-locked state
so two engineers don't clobber each other.

## Install

```bash
npm install -g fabrica-cli
```

Upgrade:

```bash
npm install -g fabrica-cli@latest
```

Or run without installing:

```bash
npx fabrica-cli --help
```

This package is a thin launcher. On install it downloads the matching prebuilt
`fabrica` binary from
[GitHub Releases](https://github.com/jpvelasco/fabrica/releases) and verifies
its SHA-256 checksum.

**Supported platforms:** linux/macOS/windows on amd64; linux/macOS on arm64.

If install scripts are blocked (`--ignore-scripts`, pnpm, locked-down CI),
`fabrica` still self-heals by fetching the binary on first run. Air-gapped?
Set `FABRICA_SKIP_AUTO_DOWNLOAD=1` and place the binary under this package's
`bin/` directory yourself.

### "allow-scripts" warning during install

If npm's `allow-scripts` policy is enabled, you may see:

```text
npm warn allow-scripts   fabrica-cli@x.y.z (postinstall: node install.js)
```

Harmless — install still succeeds; the binary downloads on first use if the
script was blocked. To silence the warning:

```bash
npm config set allow-scripts=fabrica-cli --location=user
```

### Alternative install

```bash
go install github.com/jpvelasco/fabrica@latest
```

## Quickstart

```bash
# Install
npm install -g fabrica-cli

# Config (copy example from the repo, or create fabrica.yaml)
# Set aws.region at minimum

fabrica setup --dry-run    # plan + cost for S3 state bucket + DynamoDB lock table
fabrica setup              # create state backend (once per account)
fabrica doctor             # credentials, region, bucket, lock table
fabrica status             # aggregate health (empty modules is fine)
```

Recommended first path: **foundation → DDC → Horde → deploy** (plus VCS when
you need a depot). Every create/setup command is idempotent, shows a plan +
cost estimate, and prompts before writing AWS (`--yes` skips; `--dry-run`
previews).

```bash
# Source of truth (pick one or both)
fabrica perforce create && fabrica perforce status
# or: fabrica lore create && fabrica lore status -w

# Keep cooks fast
fabrica ddc setup && fabrica ddc status --probe

# Build farm + CI over Horde
fabrica horde create && fabrica horde status
fabrica ci setup
fabrica ci trigger path/to/BuildGraph.xml --wait

# GameLift blue/green (set deploy.buildBucket in fabrica.yaml first)
fabrica deploy setup
fabrica deploy promote v1.0.0
fabrica deploy status
```

## What you get

| Module | Commands | Purpose |
|--------|----------|---------|
| Foundation | `setup`, `doctor`, `status`, `config show` | State backend + health |
| `perforce` | `create`, `status`, `destroy`, `backup`, `restore` | Helix Core on EC2 + EBS checkpoints |
| `lore` | `create`, `status`, `destroy` | Epic `loreserver` (parallel to Perforce) |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | Unreal Horde coordinator + BuildGraph jobs |
| `ddc` | `setup`, `status`, `destroy` | Unreal Cloud DDC (Jupiter), single-region V1 |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | NICE DCV cloud workstations |
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | CodeBuild orchestration over Horde |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | GameLift blue/green (alias flip) |
| `cost` | `report`, `forecast`, `alerts` | Offline cost visibility + local budgets |
| Teardown | `destroy --all` | Ordered full-stack teardown |

## Why Fabrica

Game studios aren't web apps. You need version control for terabyte asset
histories, a build farm for distributed cooks, a DDC that keeps iteration
fast, and a reliable way to stand all of it up without stitching Terraform
modules, bash scripts, and console clicks.

Fabrica owns the full lifecycle — provision, status, tear down — with
typed-phrase confirmations, recoverable partial state, and cost estimates
before every write.

## Pair with Ludus

| Tool | Role |
|------|------|
| [ludus-cli](https://www.npmjs.com/package/ludus-cli) | Build, cook, package, push UE5 dedicated servers |
| **fabrica-cli** | Provision studio infra those builds run on |

Typical loop: Fabrica stands up Horde / DDC / GameLift wiring; Ludus produces
the server package; Fabrica `deploy promote` flips the fleet.

## Requirements

- **Node.js 18+** (for this npm launcher only)
- **AWS credentials** that can create EC2, security groups, S3, DynamoDB, and
  call `sts:GetCallerIdentity`
- **Region** set in `fabrica.yaml` (`aws.region`)
- **Pre-baked AMIs** for Horde, DDC, and workstations (Fabrica configures and
  starts services via cloud-init — it does not bake the AMIs). See the repo
  docs: [horde-ami](https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md),
  [ddc-ami](https://github.com/jpvelasco/fabrica/blob/main/docs/ddc-ami.md)

## Config

Copy [`fabrica.example.yaml`](https://github.com/jpvelasco/fabrica/blob/main/fabrica.example.yaml)
to `fabrica.yaml` and set at least:

```yaml
aws:
  region: us-east-1
```

State lives in S3 (`fabrica-state-<account-id>`) + DynamoDB lock table, with a
local cache under `.fabrica/`. Module credentials (where applicable) are written
to `.fabrica/*-credentials.yaml` with mode `0600` — never embedded in EC2
UserData.

## Links

- **Source & full docs:** [github.com/jpvelasco/fabrica](https://github.com/jpvelasco/fabrica)
- **Changelog:** [CHANGELOG.md](https://github.com/jpvelasco/fabrica/blob/main/CHANGELOG.md)
- **Roadmap:** [ROADMAP.md](https://github.com/jpvelasco/fabrica/blob/main/ROADMAP.md)
- **Issues:** [github.com/jpvelasco/fabrica/issues](https://github.com/jpvelasco/fabrica/issues)
- **License:** MIT
