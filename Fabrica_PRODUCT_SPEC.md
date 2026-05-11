# Fabrica — Product Specification (DRAFT)

*Latin: "imperial workshop / the art of skillful construction"*

> Roman fabricae were imperial-scale production facilities strategically placed across the empire, producing standardized equipment at massive scale for the legions. Fabrica does the same for game studios — composable, production-grade infrastructure modules deployed at scale on AWS.

---

## Executive Summary

Fabrica is a CLI tool and infrastructure-as-code framework that provisions and manages the cloud infrastructure a game studio needs to operate: source control (Perforce Helix Core), distributed build farms, CI/CD pipelines, asset storage, and virtual workstations. Built on AWS CDK with C# .NET, it synthesizes CloudFormation stacks from opinionated, game-studio-specific constructs — delivering zero-assembly infrastructure through a guided CLI.

Fabrica is the second product in a three-part suite:

- **Ludus** — engine-specific build tool (UE5 today). Single developer, local machine.
- **Fabrica** — engine-agnostic infrastructure platform. Team-scale, cloud.
- **Classis** — engine-agnostic fleet operations. Production-scale, live games.

Each layer is independently useful. A solo dev uses Ludus alone. A team adds Fabrica. A studio shipping to players adds Classis.

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
- Run distributed builds across a build farm (not a single machine taking 6 hours)
- Set up CI/CD that triggers on commits, builds, tests, and deploys automatically
- Manage derived data caches so developers aren't rebuilding shaders locally
- Spin up cloud workstations for remote developers

...they leave Ludus territory and enter Fabrica territory.

### The Bridge: BuildGraph

Ludus's `buildgraph` command generates UE5 BuildGraph XML describing engine and game build stages as a directed acyclic graph. This is the natural handoff point:

- **Ludus** generates the BuildGraph XML and validates it
- **Fabrica** provisions a build farm that consumes that XML and executes it at scale

A developer can prototype their build locally with Ludus, then hand it off to Fabrica's build farm for distributed execution. This pattern — engine tool produces build definition, Fabrica executes it — is the template for future engine support.

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

8. **Critical IAM failures** — Jenkins grants `s3:*` and `secretsmanager:BatchGetSecretValue` on `*`. VDI grants `secretsmanager:GetSecretValue`/`PutSecretValue` and `ssm:SendCommand` on `*` to every workstation — any instance can read any secret in the account. Security groups use VPC CIDR blocks instead of security group references. These aren't configuration oversights; they're structural — Terraform HCL makes it easy to write `resources = ["*"]` and hard to generate least-privilege policies.

### The Opportunity

Fabrica can be to the Cloud Game Development Toolkit what Ludus was to "manually running RunUAT and docker build" — an opinionated, CLI-first tool with sensible defaults, guided setup, and deep domain knowledge about game studio workflows. The toolkit gives you raw materials; Fabrica gives you a working studio.

---

## Vision

A technical director at a new game studio runs:

```
fabrica setup
```

And through a guided wizard, provisions:
- A Perforce Helix Core server sized for their team
- A build farm with auto-scaling workers
- A CI pipeline triggered on Perforce submits
- A derived data cache for shader compilation
- Cost alerts and budget guardrails

The entire studio infrastructure is up in under an hour. New developers onboard by running `fabrica workstation create` and getting a cloud desktop pre-configured with the engine, the project, and all tools.

When the studio is ready to ship, they add Classis for production fleet operations — scaling, matchmaking, monitoring, blue/green deployments — all running on the infrastructure Fabrica provisioned.

---

## Target Users

| Persona | Role | How they use Fabrica |
|---------|------|---------------------|
| **Technical Director** | Decides infrastructure strategy | `fabrica setup` — initial provisioning, architecture decisions |
| **Build Engineer** | Maintains CI/CD and build systems | `fabrica build-farm scale`, `fabrica ci status` — day-2 operations |
| **Developer** | Ships game code and content | Indirect user — pushes to Perforce, builds happen automatically |
| **Studio Lead** | Budget and planning | `fabrica cost report` — cost visibility and forecasting |

The sweet spot: indie-to-mid studios (5-50 people) scaling from prototype to production. Teams that cannot afford dedicated DevOps but can't navigate raw Terraform.

---

## Core Modules

### 1. Source Control (Perforce Helix Core)

Engine-agnostic — stores any engine's assets.

- EC2-hosted Helix Core server with EBS/io2 storage
- Automated backup to S3 (daily snapshots + continuous journaling)
- Typemap configured for common game engines (binary assets, large file handling)
- Auth integration (LDAP, SAML, or local accounts)
- Monitoring: connection count, storage growth, replication lag
- Route 53 DNS (perforce.studio.internal)

### 2. Build Farm

Engine-agnostic infrastructure, engine-specific configuration via chassis adapters.

- Coordinator service (receives build definitions, distributes work)
- Auto-scaling worker fleet (spot instances for cost, on-demand for reliability)
- Build definition ingestion — BuildGraph XML from Ludus (UE5), or build commands from other chassis
- Shared derived data cache (DDC) on S3
- Multi-platform build support (Linux workers for servers, Windows workers for clients)
- Build artifact storage and retention policies

V1 uses a simpler BuildGraph executor — not Horde. Horde is Epic's build orchestrator but requires Epic organization access, has complex deployment dependencies (DocumentDB, ElastiCache, Ansible), and is the CGD Toolkit's biggest pain point. V2 offers Horde as an upgrade path for studios that need it.

### 3. CI/CD Pipeline (V2)

- Trigger on Perforce submit (via webhook)
- Pipeline stages: build → test → package → deploy
- Integration with engine-specific build tools for build steps
- Artifact promotion (dev → staging → prod)
- Notification integration (Slack, Discord, email)

### 4. Workstations (V2)

- NICE DCV cloud workstations on GPU-enabled EC2
- Pre-configured with engine, Perforce workspace, project checkout
- Auto-stop on idle to control costs
- Team templates (artist workstation, programmer workstation)

### 5. Cost Management

- Pre-provisioning cost estimates — shown before `fabrica setup` confirms
- Real-time cost dashboard per module
- Budget alerts and guardrails
- Spot instance recommendations for build workers
- Reserved instance guidance for always-on servers (Perforce, coordinator)

---

## Product Suite Architecture

### The Three Products

```
Solo Dev                    Team                      Production
─────────                   ────                      ──────────

Ludus (UE5)  ──────────┐
Unity adapter ────────┐├──→ Fabrica ──────────────→ Classis
Godot adapter ───────┐│     (infrastructure)        (fleet ops)
                     ││
                     ││     Perforce                 Fleet scaling
                     │├───→ Build farm  ───────────→ Matchmaking
                     │      CI/CD                    Monitoring
                     │      Workstations             Blue/green
                     │      Cost mgmt                Sessions
                     │
              containers + artifacts
              (engine-agnostic interface)
```

### The Airport Analogy

- **Engine-specific build tools** (Ludus and future siblings) = travel prep and luggage — each engine has unique build requirements
- **Fabrica** = the airport — shared infrastructure that serves all engines, routes, studios
- **Classis** = air traffic control — fleet management that doesn't care what engine built the container

A studio is an airline. Their games are planes — different models (engines), different routes (platforms), but they all use the same airport infrastructure.

### Product Boundaries

| Concern | Ludus / Engine Tool | Fabrica | Classis |
|---------|-------------------|---------|---------|
| Build definition | Generates BuildGraph XML, build commands | Executes builds at scale | — |
| Container images | Builds and pushes engine images | Provisions ECR, worker fleet | Deploys containers to fleets |
| Infrastructure | — | Provisions VPC, Perforce, build farm, CI/CD | Provisions GameLift fleets, matchmaking |
| Fleet operations | Test fleet for solo dev (prototype path) | — | Production fleet ops (scaling, sessions, monitoring) |
| Cost | — | Pre-provisioning estimates, budget alerts | Runtime cost per fleet, per game |

Ludus keeps its deployment commands (`ludus deploy fleet`, `ludus deploy session`) for the solo-dev prototyping path. Classis handles the production path — multi-fleet, scaling policies, matchmaking, monitoring, blue/green.

### Integration with Ludus

Fabrica and Ludus are independent tools that complement each other. Neither requires the other, but they're better together.

| Integration Point | How it works |
|-------------------|-------------|
| **BuildGraph XML** | `ludus buildgraph` generates XML → Fabrica's build farm consumes it for distributed builds |
| **Container Images** | `ludus engine push` pushes UE5 Docker images to ECR → Fabrica's build workers pull them |
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
fabrica setup                  # provision Perforce, build farm, CI
fabrica build-farm submit --graph build.xml  # submit BuildGraph to build farm
fabrica deploy promote staging # promote latest build to staging

# Production operations (Classis)
classis fleet create --from-build latest   # deploy to GameLift fleet
classis scale auto --min 2 --max 20        # configure auto-scaling
classis session create --region us-east-1  # create test session
```

---

## Multi-Engine Support

### What's Engine-Agnostic (Works for Any Engine)

- **Foundation** — VPC, subnets, NAT, Route 53. Networks don't care what game engine you use.
- **Perforce** — Helix Core stores files. It doesn't know or care if they're `.uasset`, `.unity3d`, or `.tres`.
- **Build farm infrastructure** — ECS clusters, ASGs, networking, IAM. The compute layer is the same regardless of engine.
- **Cost estimation** — AWS pricing is AWS pricing.
- **Workstations** — Windows/Linux boxes with DCV. Software packages differ, but the infrastructure is identical.

### What's Engine-Specific (Chassis Adapters)

| | UE5 | Unity | Godot |
|---|---|---|---|
| **Build system** | BuildGraph (XML DAG) | Unity Build Automation / custom | SCons |
| **Build distribution** | Horde, FastBuild, IncrediBuild | Unity Accelerator, custom | Not common at scale |
| **Caching** | DDC (Derived Data Cache), sccache | Unity Accelerator cache | No standard |
| **Container images** | Huge, licensed, UE5 installed | Unity Hub + license server | Lightweight, MIT licensed |
| **Typical project size** | 50-500GB | 10-100GB | 1-20GB |
| **Standalone tool needed?** | Yes (Ludus) — build pipeline is extremely complex | Probably not — plugin adapter sufficient | No — trivial build commands |

### The Chassis Interface

Fabrica's build farm needs five things from any engine adapter:

1. **Container image** — what worker image to run
2. **Build command** — what to execute inside the container
3. **Cache configuration** — how to configure the caching layer
4. **Artifact output** — where build outputs land
5. **Health check** — how to verify a build succeeded

That's it. Fabrica doesn't need to understand BuildGraph or SCons or Unity's build pipeline. It just needs a chassis to provide those five things.

### V1: UE5-Only

V1 targets Unreal Engine 5 studios exclusively. UE5 studios have the deepest infrastructure pain — enormous project sizes, brutal shader compilation, complex build pipelines, and near-mandatory Perforce usage.

### V2+: Engine Adapters

Not every engine needs a full Ludus-like tool. UE5's build complexity justifies a standalone product. Unity and Godot are simpler — a Fabrica plugin or lightweight adapter that knows the right build commands is probably sufficient.

---

## Architecture

### CLI-First

Like Ludus, Fabrica is a CLI tool. Infrastructure engineers interact through commands, not a web UI (though a dashboard for monitoring is in scope for later).

```
fabrica setup                     # guided first-time provisioning
fabrica status                    # health of all modules
fabrica perforce [setup|status|backup|restore]
fabrica build-farm [setup|status|scale|submit]
fabrica ci [setup|trigger|status|logs]
fabrica workstation [create|list|stop|terminate]
fabrica cost [report|forecast|alerts]
fabrica doctor                    # diagnostic checks
fabrica destroy --all             # tear down everything
```

### Technology Stack

**C# 13 on .NET 10 + AWS CDK v2**

We evaluated Go + Cloud Control API, Go + CloudFormation template generation, Terraform, Pulumi, and CDK. The decision:

- **Go + Cloud Control API** — rejected. Rebuilds Terraform's resource management, state backend, dependency resolution, and rollback from scratch. The right primitives at the wrong abstraction level.
- **Go + CloudFormation template generation** — rejected. Rebuilds CDK's construct model, template synthesis, and cross-stack references from scratch. Same mistake, one layer up.
- **C# + AWS CDK** — accepted. CDK already solves infrastructure composition, state management (via CloudFormation), IAM generation, and cross-stack references. Fabrica adds game-studio-specific L3 constructs on top.

Why C# over Go for Fabrica (when Ludus is Go):

- CDK's construct model is a class hierarchy. C# 13's OOP (records, pattern matching, required members) maps naturally to CDK constructs. Go's composition-over-inheritance makes CDK patterns awkward.
- Each product picks the language that best fits its problem. Ludus is Go because Go is perfect for a single-binary CLI that shells out to build tools. Fabrica is C# because CDK demands strong OOP.
- .NET 10 is the latest LTS, actively maintained, and has first-class AWS SDK support.

**CDK CLI is an accepted external dependency.** `fabrica doctor` checks for Node.js + CDK CLI and guides installation. This is a pragmatic tradeoff — CDK eliminates thousands of lines of custom infrastructure code in exchange for a one-time `npm install -g aws-cdk`.

### Three-Layer Architecture

```
Fabrica.Cli              — User-facing commands (setup, status, destroy, doctor, cost)
    │                      System.CommandLine (C# equivalent of Cobra)
    │
Fabrica.Constructs       — CDK L3 constructs (FoundationStack, PerforceStack, BuildFarmStack)
    │                      Game-studio-specific infrastructure patterns
    │
Fabrica.Operations       — Day-2 operations via AWS SDK (backup, scale, diagnose, cost)
    │                      Direct SDK calls for runtime operations CDK doesn't cover
    │
Fabrica.CdkApp           — CDK app entry point (reads fabrica.yaml, instantiates stacks)
                           Synthesizes CloudFormation templates, deploys via CDK CLI
```

### Why CDK Prevents the CGD Toolkit's Failures

| CGD Toolkit Problem | CDK Structural Solution |
|---------------------|------------------------|
| `s3:*` on `*`, `secretsmanager:*` on `*` | CDK grant methods (`bucket.GrantReadWrite(role)`) generate least-privilege IAM automatically. You can't accidentally grant `*`. |
| Security groups using VPC CIDRs | CDK Connections API (`server.Connections.AllowFrom(nlb, Port.Tcp(1666))`) uses SG references by default. |
| No resource naming consistency | Base construct class enforces project prefix and naming convention on every resource. |
| Inconsistent tagging across modules | Base construct class applies standard tags (`ManagedBy: fabrica`, module name, version) to all resources. |
| `terraform destroy` leaves orphans | CloudFormation stack deletion handles cleanup. Custom resources for edge cases (AMIs, DNS). |
| 10-step assembly across 3 CLIs | `fabrica setup` synthesizes and deploys CDK stacks. One command. |
| No cost visibility | Cost estimation layer queries pricing before `cdk deploy`. User confirms with full cost breakdown. |

### State Management

CloudFormation manages resource state. CDK synthesizes templates, CloudFormation tracks what's deployed, handles rollback on failure, and detects drift. No custom state backend needed.

- **No S3 state bucket** — CloudFormation is the source of truth
- **No DynamoDB lock table** — CloudFormation handles concurrent deployment protection
- **Stack outputs** — cross-module references (VPC ID from Foundation → Perforce stack) use CDK's typed cross-stack references, synthesized to CloudFormation exports

**Local config:**
- `fabrica.yaml` — user configuration (region, team size, module selections, engine preferences)
- `.fabrica/` — cached metadata, CLI state

### Module Architecture

Fabrica modules are CDK stacks composed of L3 constructs. Module-level dependency graph, imperative within modules:

- **Foundation** (VPC, subnets, NAT, DNS) — no dependencies
- **Perforce** — depends on Foundation
- **Build Farm** — depends on Foundation, optionally on Perforce

Every construct inherits from a base class that enforces:
- Project-prefixed resource naming
- Standard tag schema
- Cost metadata for estimation

### Project Structure

```
fabrica/
├── src/
│   ├── Fabrica.Cli/                  # Console app (System.CommandLine)
│   │   ├── Program.cs
│   │   ├── Commands/
│   │   │   ├── SetupCommand.cs
│   │   │   ├── StatusCommand.cs
│   │   │   ├── DoctorCommand.cs
│   │   │   ├── DestroyCommand.cs
│   │   │   └── CostCommand.cs
│   │   └── Config/
│   │       └── FabricaConfig.cs
│   │
│   ├── Fabrica.Constructs/           # CDK constructs library
│   │   ├── Foundation/
│   │   │   └── FoundationStack.cs
│   │   ├── Perforce/
│   │   │   ├── PerforceStack.cs
│   │   │   └── PerforceServer.cs     # L3 construct
│   │   ├── BuildFarm/
│   │   │   ├── BuildFarmStack.cs
│   │   │   ├── Coordinator.cs
│   │   │   └── WorkerFleet.cs
│   │   └── Shared/
│   │       ├── FabricaConstruct.cs   # Base class (enforces tags, naming)
│   │       ├── Tags.cs
│   │       └── Naming.cs
│   │
│   ├── Fabrica.Operations/           # Day-2 operations (direct SDK)
│   │   ├── Perforce/
│   │   ├── BuildFarm/
│   │   └── Cost/
│   │
│   └── Fabrica.CdkApp/              # CDK app entry point
│       └── Program.cs
│
├── tests/
├── fabrica.example.yaml
├── cdk.json
└── Fabrica.sln
```

---

## V1 Scope

### In Scope

- [ ] **Solution scaffold** — C# .NET 10, CDK v2, System.CommandLine
- [ ] **`fabrica doctor`** — prerequisite validation (AWS creds, CDK CLI, Node.js, permissions, quotas)
- [ ] **`fabrica setup`** — guided wizard for initial provisioning
- [ ] **Foundation stack** — VPC, subnets, NAT, Route 53, security group baselines
- [ ] **Perforce Helix Core stack** — EC2, EBS, NLB, backup, DNS, monitoring
- [ ] **Build Farm stack** — BuildGraph executor with coordinator + auto-scaling workers (not Horde)
- [ ] **BuildGraph integration** — ingest XML from Ludus, distribute to workers
- [ ] **Cost estimation** — surface hourly/monthly costs before provisioning
- [ ] **`fabrica status`** — health checks across all modules
- [ ] **`fabrica destroy`** — clean teardown of all stacks
- [ ] **UE5 only** — build farm is UE5-specific in V1

### Out of Scope (V1)

- Horde — V2 upgrade path. V1 uses a simpler BuildGraph executor.
- Unity / Godot adapters — engine adapter interface designed in V1, implementations in V2+
- Workstations (NICE DCV) — V2
- CI/CD pipeline — V2
- Multi-region / Perforce replicas — V2
- Multi-environment (dev/staging/prod) — V2
- Web dashboard — CLI-only for V1
- Multi-cloud — AWS only
- MCP server — add after core is stable
- Classis integration — Classis is a separate product, post-Fabrica

---

## Open Questions

1. ~~**IaC approach**~~ — **DECIDED:** C# .NET 10 + AWS CDK v2. CDK CLI is an accepted external dependency. CloudFormation manages state.
2. ~~**Horde vs. custom**~~ — **DECIDED:** V1 uses a simpler BuildGraph executor. Horde is V2 upgrade path. Too complex, requires Epic org access, CGD Toolkit's biggest pain point.
3. ~~**Build farm V1 scope**~~ — **DECIDED:** UE5-only with engine adapter interface designed for V2 expansion.
4. **Perforce licensing** — Helix Core is free for up to 5 users. How to handle licensing messaging for larger teams during setup?
5. **Naming conventions** — Fabrica-managed resources use `ManagedBy: fabrica` tag. Compatible with Ludus's `ManagedBy: ludus`. Classis will use `ManagedBy: classis`. Shared resources?
6. **Shared config** — Should `fabrica.yaml` reference `ludus.yaml` values, or should they be fully independent?
7. **Pricing model** — Is Fabrica open source (like Ludus), commercial, or open-core?
8. **Classis language** — Go (like Ludus, lightweight fleet CLI) or C# (like Fabrica, if it needs CDK constructs for GameLift)?
9. **Chassis interface specification** — Formalize the 5-point contract (image, command, cache, artifacts, health) as a versioned interface?

---

## Success Criteria

1. A studio can go from zero AWS infrastructure to a working Perforce + build farm setup in under 1 hour
2. Build times drop by 5-10x compared to single-machine builds (BuildGraph distributed across workers)
3. Total infrastructure cost is transparent and predictable before provisioning
4. Teardown is clean — `fabrica destroy --all` leaves no orphaned resources
5. A developer who knows Ludus feels immediately at home in Fabrica (same CLI patterns, same config style, same diagnostic approach)
6. Zero IAM policy uses `*` for resource scope on write actions — structural prevention via CDK grants

---

## Timeline (Rough)

| Phase | Focus | Stack |
|-------|-------|-------|
| **Phase 0** | Solution scaffold, CLI skeleton, doctor command, CDK app | C#, System.CommandLine, CDK |
| **Phase 1** | Foundation stack + Perforce stack + setup wizard | CDK constructs, CloudFormation |
| **Phase 2** | Build Farm stack (BuildGraph coordinator + workers + DDC) | CDK constructs, ECS, ASG |
| **Phase 3** | Cost estimation + status + polish | AWS Pricing API, CloudWatch |
| **Alpha** | First external users | |
