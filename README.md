# Fabrica

Game studio infrastructure as code for AWS.

Fabrica provisions the foundational systems a game development team needs to operate at scale — Perforce Helix Core repositories, Horde build farms, CI/CD pipelines, deployment targets, and cost visibility — from a single YAML configuration file.

It sits beside [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates the builds, Fabrica gives them somewhere to run.

```
fabrica doctor                 Verify your environment is ready
fabrica setup --dry-run        See what will be created and at what cost
fabrica setup                  Provision state backend
fabrica config show            Inspect the loaded configuration
```

Single binary. Zero external dependencies. Written in Go.

## Why Fabrica

Game studios have a specific set of infrastructure needs: file revision control (Perforce), distributed build orchestration (Horde), artifact storage, deployment pipelines, and the accounting to go with it. Most IaC tools are designed for web applications. Fabrica is designed for game development.

The tool takes a deliberately different approach from the usual tooling:

- **One config file** — `fabrica.yaml` describes the entire stack. No module sprawl across directories.
- **Cost upfront** — before any resource is created, you see an itemized per-month estimate.
- **No state lock surprises** — built-in distributed locking via DynamoDB so concurrent `fabrica` invocations don't conflict.
- **Safe by default** — `--dry-run` previews everything before touching AWS. Destructive operations require explicit confirmation.

## Current Status

**Phase 0 — Walking Skeleton.** The foundation is in place: CLI skeleton, AWS provider, state backend bootstrap, cost estimation, configuration system, and diagnostics. The core provisioning commands (`setup`, `doctor`) work and produce useful output.

What's _not_ here yet: Perforce provisioning, Horde build farm setup, CI/CD pipeline creation, and resource management after initial setup. Those are Phase 1.

If you're reading this during Phase 0: the tool will create S3 and DynamoDB resources for its own state management, and that's it. But the output quality, safety model, and architecture are representative of what the final tool looks like.

See [Fabrica_PRODUCT_SPEC.md](Fabrica_PRODUCT_SPEC.md) for the full product vision.

## Requirements

- Go 1.25+
- AWS credentials with permission to create S3 buckets and DynamoDB tables
- IAM permission for `sts:GetCallerIdentity`

## Building

```bash
go build -o fabrica .
```

For version information in releases, pass ldflags:

```bash
go build -ldflags="-X github.com/jpvelasco/fabrica/internal/version.Version=0.1.0 \
  -X github.com/jpvelasco/fabrica/internal/version.Commit=abc1234" -o fabrica .
```

## Quick Start

```bash
# Clone and build
git clone https://github.com/jpvelasco/fabrica.git
cd fabrica
go build -o fabrica .

# Set up configuration
cp fabrica.example.yaml fabrica.yaml
# Edit fabrica.yaml with your preferred region

# Check your environment
fabrica doctor

# Preview what setup will do (no changes)
fabrica setup --dry-run

# Create the state backend
fabrica setup

# Verify everything is healthy
fabrica doctor
```

## Commands

### `fabrica doctor`

Checks your environment and reports status for each component:

```
  [OK]   Go version:                go1.25.9
  [OK]   Fabrica version:           0.1.0 (commit abc1234)
  [OK]   AWS credentials:           authenticated
  [OK]   Region:                    us-west-2
  [OK]   S3 state bucket:           fabrica-state-123456789012
  [OK]   DynamoDB lock table:       fabrica-state-lock

All checks passed.
```

### `fabrica setup --dry-run`

Shows what will be created and estimates the cost — touching nothing in AWS:

```
  Account:  123456789012
  Region:   us-west-2

  S3 bucket:        fabrica-state-123456789012
  DynamoDB table:   fabrica-state-lock

  S3 bucket              $0.10     High
  DynamoDB table         $0.05     High
  Total:                $0.15
```

### `fabrica setup`

Creates the S3 state bucket (with versioning, encryption, and public access block) and DynamoDB lock table. Writes the detected account ID back to `fabrica.yaml`.

### `fabrica config show`

Displays the current configuration as clean YAML, including resolved resource names.

### `fabrica destroy --all`

Tear down provisioned infrastructure. Requires `--all` flag and explicit confirmation (override with `--yes`). Stub in Phase 0 — destruction logic is coming in Phase 1.

### `fabrica version`

Prints version, commit hash, Go toolchain version, and platform.

## Architecture

Strict one-way dependency flow:

```
cmd/* → internal/{config, state, cost, tags, prompt, cloud}
                                              ↓
                                      internal/cloud/aws
```

The cloud provider layer (`internal/cloud`) is an interface with a provider registry — the AWS implementation is one of potentially many. State management and cost estimation are independent of any specific provider. No command import can reach into another command's internals.

### Design Decisions

- **AWS Cloud Control API** for resource management — no Terraform, no Pulumi. This lets Fabrica speak to AWS without depending on an external DSL or state format.
- **State backend is S3 + DynamoDB** — the same pattern proven by Terraform, but using AWS-native services directly. DynamoDB provides locking; S3 provides versioned state history.
- **Cost estimation is built-in** — every resource type has an associated cost estimator. The system shows you a per-month estimate with a confidence level before any mutation happens.
- **Viper + YAML config** — familiar, mergeable, and works with `mapstructure` tags for clean struct binding.

## License

See [LICENSE](LICENSE) for details.
