# Fabrica — Product Specification (DRAFT)

*Latin: "imperial workshop / the art of skillful construction"*

> Roman fabricae were imperial-scale production facilities strategically placed across the empire, producing standardized equipment at massive scale for the legions. Fabrica does the same for game studios — composable, production-grade infrastructure modules deployed at scale on AWS.

---

## Executive Summary

Fabrica is a CLI tool and infrastructure-as-code framework that provisions and manages the cloud infrastructure a game studio needs to operate: source control (Perforce Helix Core), distributed build farms (Unreal Horde), CI/CD pipelines, deployment targets, asset storage, and virtual workstations. It picks up where Ludus leaves off — while Ludus takes a single developer from zero to a deployed game on their local machine, Fabrica scales that to a team with proper engineering infrastructure.

---

## Background: Ludus and the Gap

### What Ludus Does

[Ludus](https://github.com/jpvelasco/ludus) is a Go CLI that automates the end-to-end pipeline for deploying Unreal Engine 5 dedicated servers to AWS GameLift. A single developer can go from a fresh UE5 source checkout to a running game server in the cloud:

```
ludus setup       # interactive wizard — configure engine, game, AWS
ludus init        # validate prerequisites, fix common issues
ludus engine build   # compile UE5 from source
ludus game build     # cross-compile Linux dedicated server
ludus container build   # containerize the server
ludus deploy fleet      # deploy to GameLift
ludus deploy session    # create a game session
ludus connect           # launch client and play
```

Ludus also provides:
- **Multi-version support** — profiles for testing across UE 5.4–5.7
- **ARM64/Graviton** — cross-compile for 20-30% cheaper Graviton instances
- **BuildGraph XML generation** — `ludus buildgraph` outputs UE5 BuildGraph XML describing the full build DAG
- **MCP server** — 21 tools for AI agent orchestration of the full pipeline
- **Diagnostics** — `ludus doctor` runs 8+ checks, `ludus init --fix` auto-remediates

### Where Ludus Stops

Ludus is the "training school" — a developer's Swiss army knife. But when a studio needs to:

- Host Perforce for 10-100 developers pushing code and assets
- Run distributed builds across a Horde grid (not a single machine taking 6 hours)
- Set up CI/CD that triggers on commits, builds, tests, and deploys automatically
- Provision staging and production deployment environments
- Manage derived data caches so developers aren't rebuilding shaders locally
- Spin up cloud workstations for remote developers

...they leave Ludus territory and enter Fabrica territory.

### The Bridge: BuildGraph

Ludus's `buildgraph` command generates UE5 BuildGraph XML describing engine and game build stages as a directed acyclic graph. This is the natural handoff point:

- **Ludus** generates the BuildGraph XML and validates it
- **Fabrica** provisions a Horde grid that consumes that XML and executes it at scale

A developer can prototype their build locally with Ludus, then hand it off to Fabrica's build farm for distributed execution.

---

## Prior Art: AWS Cloud Game Development Toolkit

AWS published the [Cloud Game Development Toolkit](https://aws-games.github.io/cloud-game-development-toolkit/) (v1.1.5, actively maintained) — a collection of Terraform modules for game studio infrastructure:

| Component | What it deploys | Complexity |
|-----------|----------------|------------|
| Perforce Helix Core | EC2 + EBS/FSxN + NLB + P4Auth (ECS) + Code Review | Very high — 3 submodules, Packer AMIs required |
| Jenkins | ECS Fargate + EC2 build farm + FSx/EFS + ALB | High — 5 agent AMI types, SSH keys, EC2 Fleet plugin |
| TeamCity | ECS Fargate + Aurora Serverless v2 + EFS | High — database lock workarounds |
| Unreal Horde | ECS + DocumentDB + ElastiCache + EC2 agents + ALB | High — Epic org access required, Ansible playbooks |
| Cloud DDC | ScyllaDB on EC2 + EKS + S3 + Grafana monitoring | Very high — Kubernetes knowledge required |
| VDI Workstations | EC2 + Amazon DCV + Client VPN + Secrets Manager | High — ~$1,430/mo per workstation |
| Unity Accelerator | ECS + EFS + ALB | Moderate |

### Where the Toolkit Falls Short

1. **Terraform expertise required** — Pure HCL modules with no CLI, no guided setup, no sensible defaults. The target user (a build engineer at a game studio) must learn Terraform, Packer, and hand-wire modules together.

2. **Massive assembly tax** — Before you can even `terraform apply`, you must: build 5+ Packer AMIs (20-60 min each), create a Route53 hosted zone, provision ACM certificates, generate SSH keys and store them in Secrets Manager, hand-edit `locals.tf` with AMI IDs and ARNs. Their "Simple Build Pipeline" sample is a 10-step process across 3 CLIs and 2 web consoles.

3. **No day-2 operations** — Provisioning is step 1. The monitoring module is explicitly "pending development." No auto-scaling management, no cost tracking, no backup/restore, no upgrades, no disaster recovery. `terraform destroy` doesn't even clean up AMIs, secrets, or DNS zones.

4. **No workflow integration** — Doesn't understand Unreal's build pipeline. You manually glue BuildGraph to Horde to deployment. No config inheritance, no shared context. Jenkins plugins and cloud agents must be configured manually through the web UI after deployment.

5. **No cost awareness** — Doesn't surface cost estimates before provisioning (VDI workstations cost ~$1,430/mo each — you find out on your AWS bill). No right-sizing recommendations, no spot instance guidance for build farms.

6. **No multi-environment** — No concept of dev/staging/prod environments with promotion workflows. Their own docs warn that two samples may not deploy into the same AWS account without conflicts.

7. **No GameLift integration** — Zero deployment pipeline. You build the game and then... nothing. No fleet management, no session creation, no matchmaking. The entire deployment story is missing.

### The Opportunity

Fabrica can be to the Cloud Game Development Toolkit what Ludus was to "manually running RunUAT and docker build" — an opinionated, CLI-first tool with sensible defaults, guided setup, and deep domain knowledge about Unreal Engine workflows. The toolkit gives you raw materials; Fabrica gives you a working studio.

---

## Vision

A technical director at a new game studio runs:

```
fabrica setup
```

And through a guided wizard, provisions:
- A Perforce Helix Core server sized for their team
- A Horde build farm with auto-scaling workers
- A CI pipeline triggered on Perforce submits
- GameLift deployment infrastructure (via Ludus integration)
- A derived data cache for shader compilation
- Cost alerts and budget guardrails

The entire studio infrastructure is up in under an hour. New developers onboard by running `fabrica workstation create` and getting a cloud desktop pre-configured with the engine, the project, and all tools.

When the studio is ready to ship, `fabrica promote prod` takes their deployment from staging to production with the same infrastructure patterns they've been using since day one.

---

## Target Users

| Persona | Role | How they use Fabrica |
|---------|------|---------------------|
| **Technical Director** | Decides infrastructure strategy | `fabrica setup` — initial provisioning, architecture decisions |
| **Build Engineer** | Maintains CI/CD and build systems | `fabrica horde scale`, `fabrica ci status` — day-2 operations |
| **Developer** | Ships game code and content | Indirect user — pushes to Perforce, builds happen automatically |
| **Studio Lead** | Budget and planning | `fabrica cost report` — cost visibility and forecasting |

---

## Core Modules

### 1. Source Control (Perforce Helix Core)

- EC2-hosted Helix Core server with EBS/io2 storage
- Automated backup to S3 (daily snapshots + continuous journaling)
- Proxy servers for distributed teams (Helix Proxy in multiple regions)
- Typemap configured for UE5 (binary assets, large file handling)
- Auth integration (LDAP, SAML, or local accounts)
- Monitoring: connection count, storage growth, replication lag

### 2. Build Farm (Unreal Horde)

- Horde server (coordinator) on EC2
- Auto-scaling worker fleet (spot instances for cost, on-demand for reliability)
- BuildGraph XML ingestion from Ludus or CI
- Shared derived data cache (DDC) on S3/ElastiCache
- Multi-platform build support (Linux workers for servers, Windows workers for clients)
- Build artifact storage and retention policies

### 3. CI/CD Pipeline

- Trigger on Perforce submit (via Horde or webhook)
- Pipeline stages: build → test → package → deploy
- Integration with Ludus for GameLift deployment
- Artifact promotion (dev → staging → prod)
- Notification integration (Slack, Discord, email)

### 4. Deployment Infrastructure

- GameLift fleet management (container + EC2, via Ludus)
- Staging environments with isolated fleets
- Blue/green deployment support
- Monitoring and log aggregation (CloudWatch, optional Grafana)

### 5. Workstations (future)

- NICE DCV cloud workstations on GPU-enabled EC2
- Pre-configured with UE5, Perforce workspace, project checkout
- Auto-stop on idle to control costs
- Team templates (artist workstation, programmer workstation)

### 6. Cost Management

- Real-time cost dashboard per module
- Budget alerts and guardrails
- Spot instance recommendations for build workers
- Reserved instance guidance for always-on servers (Perforce, Horde coordinator)
- Monthly cost report generation

---

## Integration with Ludus

Fabrica and Ludus are independent tools that complement each other. Neither requires the other, but they're better together.

| Integration Point | How it works |
|-------------------|-------------|
| **BuildGraph XML** | `ludus buildgraph` generates XML → Fabrica's Horde grid consumes it for distributed builds |
| **GameLift Deployment** | Fabrica provisions the deployment infrastructure → Ludus handles container builds and fleet management within it |
| **Build Configuration** | Ludus understands UE versions, toolchains, and build configs → Fabrica's CI can invoke Ludus commands for build steps |
| **MCP** | Both tools expose MCP servers → AI agents can orchestrate the full workflow across both tools |
| **Config Convention** | Both use YAML config with similar patterns, making them feel like a family |

### Example: Full Workflow

```
# Developer machine (Ludus)
ludus setup                    # configure local build environment
ludus init --fix               # validate and fix prerequisites
ludus buildgraph -o build.xml  # generate BuildGraph XML

# Studio infrastructure (Fabrica)
fabrica setup                  # provision Perforce, Horde, CI
fabrica ci trigger --graph build.xml  # submit BuildGraph to Horde
fabrica deploy promote staging # promote latest build to staging

# Back on developer machine (Ludus)
ludus deploy session           # create test session on staging fleet
ludus connect                  # play-test
```

---

## Architecture (High-Level)

### CLI-First

Like Ludus, Fabrica is a CLI tool. Infrastructure engineers interact through commands, not a web UI (though a dashboard for monitoring is in scope for later).

```
fabrica setup                     # guided first-time provisioning
fabrica status                    # health of all modules
fabrica perforce [setup|status|backup|restore]
fabrica horde [setup|status|scale|workers]
fabrica ci [setup|trigger|status|logs]
fabrica deploy [setup|promote|status|destroy]
fabrica workstation [create|list|stop|terminate]
fabrica cost [report|forecast|alerts]
fabrica doctor                    # diagnostic checks
fabrica destroy --all             # tear down everything
```

### Infrastructure as Code

**Decision: Go + AWS Cloud Control API (single binary, zero external dependencies)**

We evaluated Terraform, Pulumi, CDK, CloudFormation, and the AWS Cloud Control API. The decision came down to two factors:

1. **Single binary UX** — `fabrica setup` must work with nothing installed except AWS credentials. No Terraform binary, no Pulumi binary, no Python runtime, no Node.js. Pulumi's Automation API looked promising (Go-native, embeddable) but requires the `pulumi` CLI binary (~200MB) on PATH — same prerequisite friction we're trying to eliminate.

2. **Full resource coverage** — The AWS Cloud Control API supports 1,100+ resource types, including all GameLift resources (ContainerFleet, ContainerGroupDefinition, MatchmakingConfiguration) that Terraform and Pulumi's classic provider are missing. New AWS resources appear automatically via CloudFormation schema updates — no waiting for provider maintainers.

**Approach:** Fabrica uses `aws-sdk-go-v2/service/cloudcontrol` — the Go SDK for Cloud Control API — to manage all AWS resources through a uniform CRUD interface:

```
CreateResource(TypeName, DesiredState)   — create any resource
GetResource(TypeName, Identifier)        — read current state
UpdateResource(TypeName, Identifier, PatchDocument)  — update via JSON Patch
DeleteResource(TypeName, Identifier)     — delete
ListResources(TypeName)                  — enumerate
```

On top of this, Fabrica builds a thin orchestration layer:
- **Resource dependency resolver** — topological sort ensures VPC before Subnet before Security Group
- **Plan/preview** — diff desired state vs. stored state before applying
- **Rollback** — compensating deletes on partial failure
- **Tagging** — all resources tagged with `ManagedBy: fabrica`, module name, version

The AWS toolkit's Terraform modules serve as reference architectures — we study what resources they create and replicate the patterns through Cloud Control API, wrapped in a guided CLI experience.

**Escape hatch:** `fabrica export --format cloudformation` generates raw CloudFormation templates from the current state, for teams that want to manage infrastructure with their own tools.

### State Management

Fabrica manages shared infrastructure (not single-machine), so state must be team-accessible, versioned, and locked against concurrent modification.

**Remote state (source of truth):**
- **S3 bucket** (`s3://fabrica-state-<account-id>/`) — encrypted (SSE-KMS), versioned (automatic history of every state change), shared across the team
- **DynamoDB table** (`fabrica-state-lock`) — prevents two people from running `fabrica deploy` simultaneously

**Local cache (fast reads):**
- `.fabrica/state.json` — cached copy of remote state, synced on each command
- `.fabrica/config.yaml` — CLI config and cached metadata

**State contents:**
- Resource inventory: type, identifier, region, properties, creation timestamp
- Deployment history: who deployed what, when, which resources changed
- Module versions: which Fabrica module version created each resource (for upgrade safety)

**Bootstrap:** `fabrica setup` creates the S3 bucket and DynamoDB table as its first action (using direct AWS SDK calls, not Cloud Control API — chicken-and-egg). All subsequent operations use the state backend.

---

## V1 Scope

### In Scope

- [ ] **CLI skeleton** — Go, Cobra, Viper (same stack as Ludus)
- [ ] **`fabrica setup`** — guided wizard for initial provisioning
- [ ] **Perforce Helix Core** — single-server deployment with automated backup
- [ ] **Horde build farm** — coordinator + auto-scaling workers
- [ ] **BuildGraph integration** — ingest XML from Ludus, submit to Horde
- [ ] **Cost estimation** — surface hourly/monthly costs before provisioning
- [ ] **`fabrica status`** — health checks across all modules
- [ ] **`fabrica doctor`** — prerequisite validation (AWS creds, permissions, quotas)
- [ ] **`fabrica destroy`** — clean teardown of all resources

### Out of Scope (V1)

- Workstations (NICE DCV) — complex GPU instance management
- Multi-region — V1 is single-region
- CI/CD pipeline — V1 focuses on build farm; CI integration comes in V2
- Web dashboard — CLI-only for V1
- Multi-cloud — AWS only
- MCP server — add after core is stable

---

## Open Questions

1. ~~**IaC approach**~~ — **DECIDED:** Go + AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`). Single binary, zero external dependencies, full resource coverage including modern GameLift. See Architecture section.
2. **Horde vs. custom** — Horde is Epic's build orchestrator but complex to deploy. Should V1 use Horde directly, or start with a simpler job distribution system?
3. **Perforce licensing** — Helix Core is free for up to 5 users. How to handle licensing for larger teams?
4. **Naming conventions** — Should Fabrica-managed AWS resources use a naming convention that's compatible with Ludus's `ManagedBy: ludus` tagging?
5. **Shared config** — Should `fabrica.yaml` be able to import/reference `ludus.yaml` values, or should they be fully independent?
6. **Pricing model** — Is Fabrica open source (like Ludus), commercial, or open-core?

---

## Success Criteria

1. A studio can go from zero AWS infrastructure to a working Perforce + Horde setup in under 1 hour
2. Build times drop by 5-10x compared to single-machine builds (BuildGraph distributed across Horde workers)
3. Total infrastructure cost is transparent and predictable before provisioning
4. Teardown is clean — `fabrica destroy --all` leaves no orphaned resources
5. A developer who knows Ludus feels immediately at home in Fabrica (same CLI patterns, same config style, same diagnostic approach)

---

## Timeline (Rough)

| Phase | Focus | Duration |
|-------|-------|----------|
| **Phase 0** | Project setup, CLI skeleton, AWS foundation | 2-3 weeks |
| **Phase 1** | Perforce Helix Core module | 3-4 weeks |
| **Phase 2** | Horde build farm module | 4-6 weeks |
| **Phase 3** | BuildGraph integration + Ludus bridge | 2-3 weeks |
| **Phase 4** | Cost management + polish | 2-3 weeks |
| **Alpha** | First external users | ~3-4 months from start |
