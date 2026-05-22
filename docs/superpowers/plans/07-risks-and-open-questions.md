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

### 4. Submit URL Resolution: Private IP, VPN, and Reachability

The `hordeHTTPClient` constructs the coordinator URL from the instance's **private IP address** retrieved via Cloud Control Get. This has several implications:

- **VPN / same-VPC required.** The machine running `fabrica horde submit` must be able to reach the instance's private IP. This works from within the VPC (same-subnet or peered), over a VPN (e.g. AWS Client VPN, Tailscale), or from a bastion. It does **not** work from a developer's laptop unless they are on VPN.
- **No public IP by default.** The instance has no public IP or Elastic IP in V1. If a studio needs internet-accessible submit (e.g. from GitHub Actions without VPN), they must attach an Elastic IP manually and update `horde.allowedCidr` to permit the runner CIDR.
- **Elastic IP (future).** A future PR could add `horde.allocateElasticIp: true` to `HordeConfig`. This is explicitly out of scope for V1.
- **Cloud Control stub.** If Cloud Control is still stubbed (as it is today), `Get` returns empty `ActualState` and the client has no IP. The submit command returns `"Horde instance has no private IP yet. Run 'fabrica horde status' to check readiness."` This is expected behavior when the stub is active.

**Resolution for V1:** Document the VPN/VPC requirement in the post-create output and in `docs/horde-ami.md`. No code change beyond the graceful empty-ActualState error.

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

### 7. AMI Drift / Misconfiguration

Fabrica trusts that the user-provided AMI is correctly built. If the AMI is outdated or misconfigured, the EC2 instance will launch but cloud-init will fail silently (or partially), leaving the coordinator unreachable.

**Failure modes and detection:**

| AMI Problem | Symptom | How Detected |
|---|---|---|
| `mongod` not starting | cloud-init exits with `"MongoDB did not become healthy within 60s"` | `/var/log/fabrica-horde-init.log` on the instance |
| `horde` unit missing | `systemctl restart horde` fails; cloud-init exits non-zero | Same log |
| Wrong `.json` config path | Horde starts but ignores the written config | Port 5000 may respond but behavior is wrong |
| Outdated Horde binary | API shape mismatch | `fabrica horde submit` gets 4xx responses |
| Wrong architecture (arm64 AMI on m7i) | Instance fails to launch | Cloud Control returns an error at create time |

**V1 approach:** Fabrica cannot introspect the AMI before launch. The readiness probe in `fabrica horde status` catches the most common failure (port 5000 never opens). The 10-minute `--wait` timeout gives a clear signal that something went wrong.

**Communication:** The post-create output includes a note directing users to check `/var/log/fabrica-horde-init.log` if the coordinator doesn't become ready. `docs/horde-ami.md` covers the verification steps.

**Future improvement:** A `fabrica horde ami verify` dry-run command (deferred) could launch the AMI in a throwaway subnet, run the health checks, and terminate it — giving confidence before production use.

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
