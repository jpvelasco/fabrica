# Changelog

All notable changes to Fabrica are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

### Changed

### Fixed

## [0.1.0] - 2026-07-15

First public-facing release of Fabrica: Phase 1 core plus Lore (v0.2), Perforce
backup/restore, and Distributed DDC V1 (single home-region).

### Added

- **Distributed DDC (Phase 2 M2 V1):** `ddc setup` / `status` / `destroy` —
  single home-region Unreal Cloud DDC (Jupiter), hybrid EBS+S3, default `zen`
  backend with optional 1-node Scylla bootstrap (not HA). Topology types for
  future multi-region; no `region add` in V1. Included in `destroy --all` and
  cost report.
- **Perforce backup / restore:** `perforce backup` / `backup list` /
  `backup delete` / `restore` — EBS-primary checkpoints via SSM, optional S3
  export, last-backup fields on `perforce status`. Create attaches an SSM
  instance profile; destroy retains the data volume (and local backups).
- **Lore module (v0.2):** `lore create` / `status` / `destroy` — AMI-first
  Epic `loreserver` on EC2 (local/EBS store); SG opens TCP+UDP 41337 and TCP
  41339; status probes `GET /health_check`. Parallel to Perforce (both coexist).
- **Foundation:** `fabrica setup` (S3 + DynamoDB state backend, idempotent),
  `fabrica status` (aggregate read-only health across modules, `--probe`),
  `fabrica doctor` (prerequisite validation), `fabrica config show`.
- **Perforce module:** `perforce create` / `status` / `destroy` / `backup` /
  `restore` — provisions Perforce Helix Core on EC2 with day-2 backup/restore.
- **Horde module:** `horde create` / `status` / `submit` / `destroy` /
  `ami build` — Unreal Horde build coordinator + BuildGraph job submission.
- **Workstation module:** `workstation create` / `list` / `stop` / `start` /
  `terminate` — NICE DCV cloud workstations.
- **CI module:** `ci setup` / `trigger` / `status` / `logs` / `destroy` —
  CodeBuild orchestration over Horde.
- **Deploy module:** `deploy setup` / `promote` / `rollback` / `status` /
  `destroy` — GameLift blue/green deployment.
- **Cost module:** `cost report` / `forecast` / `alerts` — offline,
  config-derived cost visibility and local budget guardrails.
- **Full-stack teardown:** `fabrica destroy --all` — ordered teardown of all
  modules then the state backend, backend removed only on full success.
- **Distribution:** cross-platform binaries via GoReleaser; npm package
  installs the matching binary.
- **Open-source project metadata:** MIT `LICENSE`, `CONTRIBUTING.md`,
  Contributor Covenant `CODE_OF_CONDUCT.md`, and `SECURITY.md`.

### Changed

- README Getting Started reworked around foundation → ddc → horde → deploy;
  status table includes `ddc` and accurate Perforce command surface; badges
  no longer use placeholder Codecov tokens.

[Unreleased]: https://github.com/jpvelasco/fabrica/commits/main
[0.1.0]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.0
