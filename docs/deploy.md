# Deploy Module

`fabrica deploy` orchestrates GameLift deployment of the UE5 dedicated-server
builds produced by the CI/Horde pipeline. It owns the build-to-deploy path;
live runtime fleet operations (scaling, matchmaking, sessions) are outside
Fabrica scope.

## Commands

| Command | Purpose |
|---------|---------|
| `fabrica deploy setup` | Provision the IAM role (GameLift→S3 read) + GameLift alias. Idempotent. |
| `fabrica deploy promote <build-version>` | Register a build from S3, create a new fleet, wait for ACTIVE, flip the alias (blue/green). |
| `fabrica deploy rollback` | Flip the alias back to the most-recent retained fleet. |
| `fabrica deploy status` | Show the alias, active fleet, and rollback candidates with live fleet status. |
| `fabrica deploy destroy [--all]` | Delete fleets + builds; `--all` also removes the alias + role. |

## Prerequisites

1. `fabrica setup` (state backend).
2. `deploy.buildBucket` set in `fabrica.yaml`.
3. The packaged server build uploaded to S3 (by CI/Horde), by convention at
   `s3://<buildBucket>/builds/<build-version>/server.zip` (override with
   `--s3-bucket`/`--s3-key`).

## Blue/green and rollback

`promote` always creates a **new fleet** and flips the alias only once the fleet
is `ACTIVE`. The previously-active fleet is **retained** so `rollback` is an
instant alias flip — no re-provisioning. `destroy` (without `--all`) leaves the
alias and role in place so the alias your game backend references survives
teardown.

## Architecture notes

- GameLift `Build`, `Fleet`, and `Alias` are created through the Cloud Control
  API. Fleet **activation** (20–40 min) is tracked through the
  `cloud.GameLiftManager` SDK auxiliary interface (`FleetStatus`/`FleetEvents`)
  because the blocking Cloud Control waiter cannot surface fleet phases or
  activation-failure events. Fleet creation uses a non-blocking Cloud Control
  path (`CreateFleetAsync`) that returns as soon as the FleetId is assigned.
- The deploy module reuses the shared `cmd/internal/teardown` engine via its
  `ResourceOrder` hook to delete GameLift resources in dependency order.

## Private E2E: minimal build fixture

`promote` registers whatever zip it is pointed at — GameLift does not inspect
the contents. To exercise `promote`/`rollback` without a real UE build, upload a
minimal `server.zip` with no secrets or studio content:

```
server.zip
└── ProcessLaunchers/
    └── linux/
        └── game-session-worker   # any executable placeholder
```

The default `deploy.launchPath` is `/local/game/ServerApp` — GameLift
extracts the zip into the fleet instance's `/local/game/` and runs the
launcher at that path, so the archive must contain
`game/ServerApp`. A 1–2 KB placeholder script is enough for the Fabrica
control-plane checks (build registered, fleet created, alias flipped); a
fleet whose launcher fails will not reach `ACTIVE`, so use a stub that
exits 0 to keep the E2E fast:

```
mkdir -p /tmp/fx/game
printf '#!/bin/sh\nexit 0\n' > /tmp/fx/game/ServerApp
chmod +x /tmp/fx/game/ServerApp
(cd /tmp/fx && zip -r server.zip game)
aws s3 cp /tmp/fx/server.zip s3://<deploy.buildBucket>/builds/e2e-fixture-<ts>/server.zip
```

Checklist (live E2E playbook step):

1. `fabrica deploy setup` — alias + IAM role.
2. `fabrica deploy promote e2e-fixture-<ts> --yes` — build registered, fleet
   created; poll `fabrica deploy status` until the new fleet is `ACTIVE`
   (20–40 min for fleet activation).
3. `fabrica deploy rollback` — alias flips back to the retained fleet;
   `fabrica deploy status` shows the prior fleet active again.
4. `fabrica deploy destroy --all` — fleets, builds, alias, and role removed.

Skip this section when `deploy.buildBucket` is unset; the live E2E should
note the skip rather than fail.

## Out of scope (V1)

Scaling policies, FlexMatch matchmaking, game-session management, deep runtime
monitoring; auto-draining sessions on fleet delete; multi-region
fleets; container/Anywhere/Realtime fleets.
