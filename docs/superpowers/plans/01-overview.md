# Horde V1 Implementation Plan — Overview

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provision and manage a single Unreal Horde coordinator on EC2 — create, check health, and submit BuildGraph jobs (PR #1).

**Architecture:** AMI-first provisioning: user bakes an AMI containing MongoDB 7, Redis 6.2, and the Horde server as systemd units; Fabrica's cloud-init only configures and starts services. Two AWS resources are created in order: EC2 Security Group then EC2 Instance. State is written after each so partial failures are recoverable.

**Tech Stack:** Go, AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`), Cobra, `text/template` for cloud-init, `encoding/xml` for BuildGraph parsing, standard `net/http` for HordeClient.

**Design spec:** `docs/superpowers/specs/2026-05-21-horde-v1-design.md`

---

## PR #1 Scope

| In scope | Out of scope |
|---|---|
| `fabrica horde create` | `fabrica horde destroy` (PR #2) |
| `fabrica horde status` | Agent fleet / Auto Scaling Group |
| `fabrica horde submit` | OIDC authentication |
| `internal/horde` plan layer | `fabrica horde logs`, `fabrica horde cancel` |
| `docs/horde-ami.md` (done) | Multi-region, load balancer, DocumentDB |

---

## Key Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Instance family | `m7i` (Sapphire Rapids) | Current generation; m7i.xlarge default, m7i.2xlarge for >10 agents |
| AMI provisioning | User bakes AMI | No GitHub PAT in UserData (readable via `ec2:DescribeInstanceAttribute`) |
| MongoDB + Redis | On-instance (same EC2) | No extra AWS cost; sufficient for coordinator-only V1 |
| SG default CIDR | `10.0.0.0/8` | Restrictive by default; warning shown if set to `0.0.0.0/0` |
| HordeClient location | `cmd/horde/submit/client.go` | Only submit needs it in V1; move to `internal/` when a second command needs it |
| submit default | Fire-and-forget | `--wait`/`-w` for CI pipelines |
| Config promotion | `Config.Horde any` → `HordeConfig` | PR #1 is the only change outside `cmd/horde/` and `internal/horde/` |

---

## Ports

| Port | Protocol | Purpose |
|---|---|---|
| 5000 | TCP | Horde HTTP API + web UI |
| 5002 | TCP | Horde HTTP/2 — agent gRPC |

MongoDB (27017) and Redis (6379) are localhost-only — no ingress rules.

---

## Cost Estimate (m7i.xlarge default)

| Resource | Cost/mo |
|---|---|
| EC2 m7i.xlarge | $147.17 |
| EBS gp3 100 GiB | $8.00 |
| **Total** | **$155.17** |

*Consider m7i.2xlarge ($294.34/mo) for production workloads with >10 agents.*

---

## What's Not in V1

The following are explicitly deferred — do not design for them now.

| Deferred | Notes |
|---|---|
| `fabrica horde destroy` | PR #2; follows perforce destroy pattern |
| Agent fleet / Auto Scaling Group | Coordinator-only V1; agents are future work |
| `fabrica horde ami build` | AMI creation is manual (see `docs/horde-ami.md`); a `fabrica horde ami build` command is out of scope |
| OIDC / SSO authentication | Web UI admin setup is manual in V1 |
| `fabrica horde logs` | Requires REST client in `internal/` — only moves there when a second command needs it |
| `fabrica horde cancel` | Same dependency as logs |
| Multi-region coordinator | Single-region only in V1 |
| Load balancer / high availability | Single EC2 instance; no ELB |
| DocumentDB / Atlas | On-instance MongoDB is sufficient for V1 coordinator load |
| Elastic IP for coordinator | Private IP only; public access requires VPN or VPC peering |
| Cost alerting / forecasting | `internal/cost` estimators exist but no alert wiring |
| `fabrica horde scale` | No ASG in V1 |

---

## File Index

- `02-module-structure.md` — folder layout and package responsibilities
- `03-create-command.md` — `fabrica horde create` implementation plan
- `04-status-command.md` — `fabrica horde status` implementation plan
- `05-submit-command.md` — `fabrica horde submit` implementation plan
- `06-testing-strategy.md` — overall test approach and key scenarios
- `07-risks-and-open-questions.md` — risks, open questions, PR #2 notes
- `08-implementation-order.md` — recommended step-by-step build sequence
