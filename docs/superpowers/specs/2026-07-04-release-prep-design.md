# Release Prep — Design (Phase 1, Milestone 5, Sub-project E)

Status: approved for implementation
Date: 2026-07-04

## Goal

Set up the complete release *machinery* for Fabrica — dormant until a `v*` tag is
pushed — so a future release publishes cross-platform binaries + an npm package,
exactly like the `ludus-cli` pattern. **This sub-project performs NO actual
release:** no git tag, no `npm publish`, no GitHub Release, no npm-org setup. It
lands as a reviewable PR containing the scaffolding, verified by snapshot build
only.

## Hard guardrail (JP's explicit constraint)

No deployments/releases of any kind until Fabrica is "completely done" and JP
gives the word. For this sub-project that means:
- The release workflow fires **only** on a `v*` tag push; **no tag is created**.
- Verification is **snapshot-only** (`goreleaser release --snapshot --clean`) —
  builds every artifact locally, publishes nothing.
- We do **not**: create/push a git tag, run `npm publish`, create the npm
  org/package, set up the npm trusted publisher, or create a GitHub Release.
- Those are the actual-release steps; they wait for explicit go-ahead (release
  day, via the `release-npm` skill).

## Scope decision (locked)

- **v0.1 scope = the current 6 modules** (perforce, horde, workstation, ci,
  deploy, cost) + `destroy --all` + the foundation (setup/doctor/status/config).
  The **Lore module is v0.2** — it stays on its own `feat/lore-module` branch,
  untouched by this work.
- **Distribution = the Ludus pattern:** GoReleaser builds Go binaries; an npm
  shim package downloads the matching binary at install time (`npx fabrica` /
  `npm i -g <pkg>`), same mechanism as `ludus-cli`.
- **npm package name:** DEFERRED to release day (candidates: `fabrica-cli` to
  match Ludus, or scoped `@jpvelasco/fabrica`). The scaffolding uses a clearly
  marked placeholder that release day resolves.

## Reference implementation

This is a faithful port of the proven Ludus pipeline at `/f/source/ludus`
(`.goreleaser.yaml`, `npm/`, `scripts/embed-checksums.js`,
`.github/workflows/release.yml`). Adapt ludus→fabrica (repo, binary name, module
path); keep the mechanism identical. Ludus is the authority for any detail this
spec underspecifies.

### The pipeline (end to end)

```
git tag vX.Y.Z + push
  → release.yml (on: push tags v*)
    → GoReleaser: build binaries (linux/darwin/windows × amd64/arm64, ignore win/arm64)
                  + GitHub Release + dist/checksums.txt   [auth: GITHUB_TOKEN]
    → scripts/embed-checksums.js dist/checksums.txt npm/package.json
                  (writes SHA-256s into package.json "binaryChecksums")
    → npm version <tag-without-v> --no-git-tag-version
    → npm publish --access public                          [auth: npm OIDC/token]
  → at user install time: npm postinstall runs install.js →
      downloads the matching binary from the GitHub Release, verifies checksum;
      run.js execs it.
```

## Components (all new; ported + adapted from Ludus)

### 1. `.goreleaser.yaml` (repo root)
Mirror Ludus's, adapted:
- `version: 2`
- `builds`: `id: fabrica`, `main: .`, `binary: fabrica`, `CGO_ENABLED=0`,
  `ldflags: -s -w -X github.com/jpvelasco/fabrica/internal/version.Version={{.Version}}`
  (add `-X ...Commit={{.ShortCommit}}` too — Fabrica's version pkg has both
  `Version` and `Commit`, unlike Ludus which only injects Version).
  `goos: [linux, windows, darwin]`, `goarch: [amd64, arm64]`,
  `ignore: goos:windows/goarch:arm64`.
- `archives`: `formats: [tar.gz]`, windows override `zip`,
  `name_template: "fabrica_{{ .Version }}_{{ .Os }}_{{ .Arch }}"`.
- `checksum`: `name_template: checksums.txt`.
- `changelog`: sort asc, exclude `^docs:`/`^test:`/`^ci:`.

The `name_template` MUST match `getArchiveName()` in `install.js` — verify the
two agree (same `fabrica_<version>_<os>_<arch>` + ext) as an explicit step.

### 2. `npm/` shim package (new directory)
Port each file from `/f/source/ludus/npm/`, renaming ludus→fabrica:
- **`package.json`** — `name`: PLACEHOLDER `"fabrica-cli"` with a comment/spec
  note that release day confirms the final name; `version: "0.0.0"` (release
  workflow sets it via `npm version`); `bin: { "fabrica": "run.js" }`;
  `scripts.postinstall: "node install.js"`; `os: [linux,darwin,win32]`;
  `cpu: [x64,arm64]`; `files: [install.js, run.js, bin/]`; `binaryChecksums: {}`;
  fabrica-appropriate `description`/`keywords` (aws, gamedev, perforce, horde,
  gamelift, iac, cli, devops); `repository` → `jpvelasco/fabrica`; MIT.
- **`run.js`** — the launcher: resolves `bin/<binaryName()>`, self-heal via
  `ensureBinary({silent:true})` (with a `FABRICA_SKIP_AUTO_DOWNLOAD` escape
  hatch mirroring Ludus's `LUDUS_SKIP_AUTO_DOWNLOAD`), spawns with `stdio:inherit`,
  forwards SIGINT/SIGTERM/SIGHUP, propagates exit code. Adapt error strings to
  `fabrica`.
- **`install.js`** — `REPO="jpvelasco/fabrica"`, `binaryName()` →
  `fabrica.exe`/`fabrica`, PLATFORM_MAP/ARCH_MAP, `getArchiveName()`,
  download-from-`releases/download` with redirect handling, SHA-256
  `verifyChecksum` against `package.json` `binaryChecksums`, version marker.
- **`install.test.js`** — port Ludus's `node --test` coverage (binaryName,
  archive-name, checksum, unsupported-platform).
- **`.npmignore`**, **`README.md`** (npm-facing install/usage for fabrica).

### 3. `scripts/embed-checksums.js` (new)
Port verbatim from Ludus: reads `dist/checksums.txt`, maps each archive → its
SHA-256, writes the `binaryChecksums` object into `npm/package.json`. Runs in CI
between GoReleaser and `npm publish`.

### 4. `.github/workflows/release.yml` (new)
Port Ludus's:
- `on: push: tags: ["v*"]`; `permissions: contents: write, id-token: write`.
- Steps: checkout (fetch-depth 0) → setup-go (go-version-file go.mod) →
  goreleaser-action `release --clean` (env `GITHUB_TOKEN`) → setup-node
  (node 24, registry npmjs) → `node scripts/embed-checksums.js dist/checksums.txt
  npm/package.json` → in `npm/`: `npm version "${GITHUB_REF_NAME#v}"
  --no-git-tag-version` && `npm publish --access public`.
- Pin all action SHAs (match the style already used in `ci.yml`).
- **npm auth:** decided at release day (OIDC trusted publisher preferred, per the
  npm-init/release-npm skills; `NPM_TOKEN` secret is the fallback). The workflow
  is written for OIDC (`id-token: write` present); if a token is needed instead,
  that's a one-line env addition at release time. Note this in the workflow as a
  comment — do NOT add a secret now.

### 5. `CHANGELOG.md` (new, repo root)
"Keep a Changelog" format. An `## [Unreleased]` section documenting the shipped
v0.1 surface grouped as Added: the foundation (setup/doctor/status/config show),
and the six modules + `destroy --all`, the cost gate + E2E harness as tooling.
No version number, no date — those get filled when the tag is cut. Link
references at the bottom left as a template.

### 6. CI snapshot-validation job (modify `.github/workflows/ci.yml`)
Add a `goreleaser` job that runs `goreleaser build --snapshot --clean`
(build-only; **never** `release`, never publishes) on push/PR, so a future
`.goreleaser.yaml` edit that breaks the build is caught at PR time. Uses
goreleaser-action with `args: build --snapshot --clean`. Also lint the npm shim:
`node --test` in `npm/` (runs `install.test.js`).

### 7. Docs
- **README.md** — add an "Install" section for the npm path (`npx <pkg>` /
  `npm i -g <pkg>`) with a placeholder-name note, plus the `go install` fallback.
  The doc-drift guard is unaffected (no command changes).
- **ROADMAP.md** — M5 line: `⬜ v0.1 / v1.0 release preparation` →
  `✅ Release machinery (GoReleaser + npm shim, Ludus pattern) — dormant until a v* tag; no release cut`.
- **CLAUDE.md** — add a short "Releasing" section: the pipeline, that it's
  dormant, and that cutting a release is a deliberate tag push (deferred).

## Verification (snapshot-only; publishes nothing)

- `goreleaser check` — config is valid.
- `goreleaser release --snapshot --clean` — builds all platform archives +
  checksums into `dist/` with NO publish/tag. Confirm the archive names match
  `install.js`'s `getArchiveName()`.
- `goreleaser build --snapshot --clean` — the CI job's command works.
- `node --test` in `npm/` — the shim's unit tests pass.
- A local end-to-end dry-run: run embed-checksums against the snapshot
  `dist/checksums.txt` into a COPY of package.json, confirm it populates
  `binaryChecksums` correctly (do not commit the populated copy).
- Full existing gate: `go build ./... && go test ./... && go vet ./... &&
  gofmt -l .` clean; `golangci-lint run ./...` 0 issues. (Go code is unchanged —
  only version ldflags are exercised.)
- **Confirm no tag exists and no publish occurred:** `git tag` empty;
  `release.yml` only triggers on `v*`.

## Prerequisites for the ACTUAL release (documented, NOT done now)

Recorded in the spec + CLAUDE.md so release day is unambiguous:
1. Decide the npm package name; set it in `npm/package.json`.
2. Set up the npm org/package + trusted publisher (OIDC) — the `npm-init` skill.
3. Finalize CHANGELOG `[Unreleased]` → `[0.1.0]` with the date.
4. `git tag v0.1.0 && git push origin v0.1.0` — this is the trigger; done only on
   JP's explicit go-ahead (the `release-npm` skill drives it).

## Out of scope (YAGNI / constraint)

- Any actual release action (tag/publish/GitHub Release/npm org) — deferred.
- Lore module (v0.2, separate branch).
- Homebrew tap / other channels (Ludus doesn't; neither do we for v0.1).
- Changing the 6 modules or their code.

## Docs / roadmap updates on completion

- ROADMAP M5-E → ✅ (machinery ready, dormant, no release cut).
- CLAUDE.md "Releasing" section added.
- README install section added.
