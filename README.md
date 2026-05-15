# Fabrica

Provision game studio cloud infrastructure on AWS with a single CLI tool.

Sister tool to [Ludus](https://github.com/jpvelasco/ludus).

Fabrica stands up the foundational systems a game studio needs to operate at scale: Perforce Helix Core, Horde build farms, CI/CD pipelines, deployment targets, and cost management — all from one configuration file.

Single binary. Zero external dependencies.

## Status

**Early development (Phase 0 — Walking Skeleton).** The CLI skeleton, AWS provider interface, state backend bootstrap, cost estimation, and diagnostic system are in place. Module provisioning (Perforce, Horde) is planned for Phase 1.

See [Fabrica_PRODUCT_SPEC.md](Fabrica_PRODUCT_SPEC.md) for the full vision.

## Requirements

- Go 1.25+
- AWS credentials configured (SDK default chain or named profile)

## Building

```bash
go build -o fabrica .
```

## Usage

```
fabrica version                  # Print version information
fabrica doctor                   # Run diagnostic checks
fabrica config show              # Show current configuration
fabrica setup --dry-run          # Preview what setup would do (cost estimate, resource names)
fabrica setup                    # Bootstrap state backend (S3 + DynamoDB)
fabrica destroy --all            # Tear down infrastructure (requires confirmation)
```

Copy `fabrica.example.yaml` to `fabrica.yaml` and configure before running `setup`.

```bash
cp fabrica.example.yaml fabrica.yaml
```

## Commands

| Command | Description |
|---------|-------------|
| `fabrica doctor` | Diagnostics: Go version, AWS credentials, region, Fabrica version, state backend health |
| `fabrica setup` | Provision state backend (S3 bucket + DynamoDB lock table) |
| `fabrica setup --dry-run` | Preview resources and cost estimate without making changes |
| `fabrica config show` | Display loaded configuration as YAML |
| `fabrica destroy --all` | Tear down provisioned infrastructure (stub in Phase 0) |
| `fabrica version` | Print version, commit, Go version, and OS/arch |

All commands support `--verbose`, `--json`, `--dry-run`, `--yes`, `--profile`, and `--config`.

## Architecture

```
cmd/* -> internal/{config, state, cost, tags, prompt, cloud}
                                                v
                                        internal/cloud/aws
```

Strict one-way dependency flow. `internal/cloud/*` never imports `internal/state`, `internal/cost`, or `cmd/*`.

## License

See [LICENSE](LICENSE) for details.
