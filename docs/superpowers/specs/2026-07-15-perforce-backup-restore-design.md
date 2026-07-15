# Perforce Backup / Restore Design

**Date:** 2026-07-15  
**Status:** Approved — implementation in progress  
**Scope:** `fabrica perforce backup|backup list|backup delete|restore` (V1)

---

## Goal

Add safe, reliable backup and restore to the existing Perforce module so studios can protect Helix Core data without leaving Fabrica.

## Locked V1 scope

- Commands: `perforce backup`, `perforce backup list`, `perforce backup delete`, `perforce restore`
- Primary store: attached EBS under `/hxdepots/fabrica-backups`
- Optional S3 export
- Server-side checkpoint + journal packaging (Helix admin ops)
- Simple versioning via `metadata.json` beside each backup
- Server-only (no client helpers)

## Out of scope

- Scheduled backups, cross-region replication, PITR UI
- Advanced encryption/compression
- DR rehydrate onto a new instance from orphan volume/S3
- Auto-migration of pre-SSM instances

## Architecture

- `internal/perforce/backup.go` — pure plan layer: IDs, metadata, shell scripts
- `cloud.RemoteRunner` — SSM Run Command auxiliary interface (like `EC2InstanceManager`)
- Create always attaches an IAM instance profile with `AmazonSSMManagedInstanceCore`
- State caches `lastBackupId` / `lastBackupAt` on the EC2 resource Properties
- Inventory of truth lives on disk (`metadata.json`); list/delete/restore read via SSM

## Commands

```bash
fabrica perforce backup [--name <name>] [--description <text>] [--no-s3]
fabrica perforce backup list [--json]
fabrica perforce backup delete <backup-id>
fabrica perforce restore <backup-id> [--force] [--yes]
```

## Destroy safety

EBS uses `DeleteOnTermination: false`. Destroy removes instance/SG/IAM only. Local backups on the volume and any S3 exports are retained. Messaging must warn about orphan volume cost.

## Config

```yaml
perforce:
  backup:
    path: /hxdepots/fabrica-backups
    s3Export: false
    s3Bucket: ""
    s3Prefix: perforce-backups/
```
