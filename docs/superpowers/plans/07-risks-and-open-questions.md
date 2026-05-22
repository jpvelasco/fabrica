# Horde V1 — Risks and Open Questions

## Known Risks

### 1. Cost Registry Duplicate Registration (HIGH — must resolve before coding)

`internal/perforce/cost.go` registers `AWS::EC2::Instance` and `AWS::EC2::Volume` in `cost.Global`. If `internal/horde/cost.go` tries to register the same TypeNames, `cost.Global.Register` panics at startup.

**Resolution:** Add m7i prices to `ec2InstancePrices` in `internal/perforce/cost.go`. Do not create a separate `internal/horde/cost.go`. This is covered in Task 4 of `03-create-command.md`.

**Long-term:** Factor the shared EC2 price table into `internal/cost/estimators_ec2.go` (deferred — not PR #1 scope).

---

### 2. Port Numbers in Status Command

The status command needs to probe port 5000 and construct `hordeUrl`/`hordeGrpc` URLs. These ports are configured in `HordeConfig` at create time but may not be available at status time if the user doesn't have `fabrica.yaml` in the working directory.

**Resolution:** Read `c.runtime.Config.Horde.Port` and `GRPCPort` with defaults (5000, 5002). If `Config` is nil or ports are zero, use defaults. This matches how the perforce status command handles the version field.

---

### 3. AMI Not Available in Region

`horde.amiId` is region-scoped. If the user specifies an AMI from a different region, `createResource` will fail with a Cloud Control error.

**Resolution:** Document in `docs/horde-ami.md` (done). No code change needed — the Cloud Control error message is descriptive enough. The Common Pitfalls table in the AMI doc covers this.

---

### 4. Submit URL Resolution Without Live AWS

The `hordeHTTPClient` resolves the coordinator IP by calling `provider.Resources().Get` on the EC2 instance (Cloud Control). If Cloud Control is still stubbed (as it is today), Get returns empty `ActualState` and the client has no IP.

**Resolution:** The submit command falls back to `nil` `ActualState` gracefully — if `PrivateIpAddress` is empty in ActualState, it returns an error: `"Horde instance has no private IP yet. Run 'fabrica horde status' to check readiness."` This is not a test failure; it's the expected behavior when Cloud Control is stubbed.

**For integration testing** once Cloud Control is live: the submit command will resolve correctly.

---

### 5. `config.Config.Horde` Type Change May Break Existing Serialization

Changing `Config.Horde` from `any` to `HordeConfig` will cause Viper to reject existing `fabrica.yaml` files that have an unrecognized horde section format.

**Resolution:** This is a clean slate — Horde has no existing users. The `fileConfig` struct change is safe. Users who have `horde: {}` or no `horde:` key at all will get zero-value `HordeConfig` (AmiID empty → `NewCreatePlan` returns a clear error).

---

### 6. Horde REST API Contract (submit)

The actual Horde REST API paths and request/response shapes are based on the official Horde server source. `POST /api/v1/jobs` and `GET /api/v1/jobs/{id}` are the documented endpoints.

**Risk:** API shape may differ between Horde versions. The `client.go` implementation uses struct-based JSON marshaling — if the API changes, a compile-time error won't fire; only a runtime 4xx will.

**Resolution:** Document the tested Horde version in `cmd/horde/submit/client.go`. For V1, fire-and-forget is the default; `--wait` is opt-in. If the GET endpoint shape changes, only `--wait` is affected.

---

## Open Questions

### Q1: Should `Config.Horde` import `internal/horde`?

Promoting `Config.Horde` from `any` to `HordeConfig` requires `internal/config` to import `internal/horde`. This creates a dependency: `internal/config` → `internal/horde`.

Check this doesn't violate the dependency rule:
```bash
go list -deps ./internal/cloud/...
```
The dependency flow is `cmd/* → internal/*`, and `internal/config` is already imported by `internal/perforce`. Adding `internal/horde` as an import of `internal/config` is fine as long as `internal/horde` doesn't import `internal/config` back (circular).

**Simpler alternative:** Define `HordeConfig` directly in `internal/config/config.go` (same file as `PerforceConfig`) instead of importing from `internal/horde`. This avoids the cross-package dependency entirely and is the preferred approach — matching the pattern where `PerforceConfig` lives in `internal/config`.

**Decision:** Define `HordeConfig` in `internal/config/config.go`. `internal/horde/config.go` only defines `VPCResolver`. `internal/horde/plan.go` imports `internal/config` to receive `config.HordeConfig`.

---

### Q2: Service account token for submit

The design spec says `cmd/horde/submit/client.go` authenticates with `Authorization: ServiceAccount <token>`. However, `fabrica horde create` does not create a service account or write a token to `.fabrica/horde-credentials.yaml` — the admin account is set up manually through the web UI.

**Resolution for V1:** The `horde_service_token` field in `.fabrica/horde-credentials.yaml` is optional. If absent or empty, the auth header is omitted. Users who haven't set up a service account will get a 401 from Horde; the error message explains where to configure it.

This means the initial `writeCredentials()` in create only writes `mongodb_password`. The token field is added by the user after manual admin setup.

---

## PR #2 Notes (destroy)

When `fabrica horde destroy` is added in PR #2, the pattern follows `cmd/perforce/destroy/`:

- Read state → confirm phrase `"destroy horde <account>"`
- Delete EC2 instance (skip if already terminated, per perforce hardening pattern)
- Delete Security Group
- Clear horde module from state
- Delete `.fabrica/horde-credentials.yaml`

The destroy command does NOT delete the user-built AMI — that was created outside Fabrica.

`cmd/horde/horde.go` will gain a `destroy` subcommand import. No changes to `internal/horde/` should be needed for destroy (the plan layer for destroy is thin — just Cloud Control desired-state JSON for a delete operation).
