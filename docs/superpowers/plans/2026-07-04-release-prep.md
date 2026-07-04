# Release Prep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Set up Fabrica's release machinery — GoReleaser cross-platform binaries + an npm shim package (the `ludus-cli` pattern) — wired DORMANT (fires only on a future `v*` tag). No release, tag, or publish happens in this work.

**Architecture:** Faithful port of the proven Ludus pipeline (`/f/source/ludus`): `.goreleaser.yaml` builds binaries + a GitHub Release + checksums; a `release.yml` workflow (tag-triggered) runs GoReleaser, embeds checksums into `npm/package.json`, and `npm publish`es the shim; the shim's `install.js` downloads the matching binary from the GitHub Release at install time. Every file is adapted ludus→fabrica.

**Tech Stack:** Go 1.25.11, GoReleaser v2, Node.js (npm shim, no runtime deps — stdlib `https`/`crypto`/`child_process`), GitHub Actions. Markdown/YAML/TOML config.

## Global Constraints

- **NO actual release in this work.** No `git tag`, no `npm publish`, no `gh release`, no npm-org/trusted-publisher setup, no GitHub Release. `release.yml` triggers ONLY on `push: tags: v*` and we create no tag. Verification is snapshot-only (`goreleaser --snapshot`, which publishes nothing).
- **Port, don't invent.** Files are adapted from `/f/source/ludus` (`.goreleaser.yaml`, `npm/{package.json,run.js,install.js,install.test.js,.npmignore}`, `scripts/embed-checksums.js`, `.github/workflows/release.yml`). Swap `ludus`→`fabrica`, repo `jpvelasco/ludus`→`jpvelasco/fabrica`, binary `ludus`→`fabrica`, env `LUDUS_SKIP_AUTO_DOWNLOAD`→`FABRICA_SKIP_AUTO_DOWNLOAD`. Keep mechanism identical. Full adapted code is in each task — use it verbatim.
- **npm package name is a release-day decision** — use placeholder `"fabrica-cli"` in `package.json` with a spec-referenced comment; do NOT finalize.
- **Fabrica's version pkg has BOTH `Version` and `Commit`** (`internal/version`) — GoReleaser ldflags inject both (Ludus injects only Version).
- Module path `github.com/jpvelasco/fabrica`; binary is built from repo root (`main: .`).
- GoReleaser archive `name_template` MUST equal `install.js`'s `getArchiveName()` output: `fabrica_<version>_<os>_<arch>.<ext>` (ext = `tar.gz`, or `zip` on windows).
- Pin all GitHub Action SHAs (match `.github/workflows/ci.yml` style).
- Quality gate: `go build ./... && go test ./... && go vet ./... && gofmt -l .` clean; `golangci-lint run ./...` 0 issues. (Go code unchanged; only version ldflags exercised.) npm shim: `node --test` in `npm/` passes.
- LF line endings; conventional-commit messages; git hooks active (no `--no-verify`).

## File Structure

**New files:**
- `.goreleaser.yaml` (repo root) — build/archive/checksum config.
- `npm/package.json`, `npm/run.js`, `npm/install.js`, `npm/install.test.js`, `npm/.npmignore`, `npm/README.md` — the shim package.
- `scripts/embed-checksums.js` — checksums.txt → package.json injector.
- `.github/workflows/release.yml` — dormant tag-triggered release pipeline.
- `CHANGELOG.md` (repo root) — Keep a Changelog, `[Unreleased]`.

**Modified files:**
- `.github/workflows/ci.yml` — add a `goreleaser build --snapshot` validation job + npm shim `node --test`.
- `README.md` — Install section (npm + go install).
- `ROADMAP.md`, `CLAUDE.md` — status + a "Releasing" section.

**Build order:** Task 1 (GoReleaser config) → Task 2 (npm shim) → Task 3 (embed-checksums + release.yml + CI snapshot job) → Task 4 (CHANGELOG + docs + final gate). Task 3's release.yml references Task 1's dist artifacts + Task 2's package.json + Task 3's embed script; Task 2's install.js archive-name must match Task 1's name_template (verified in Task 2).

---

### Task 1: GoReleaser config

**Files:**
- Create: `.goreleaser.yaml`

**Interfaces:**
- Consumes: `internal/version.{Version,Commit}` (ldflags targets), `main.go` at repo root.
- Produces: a GoReleaser config that builds `fabrica` for linux/darwin/windows × amd64/arm64 (ignore windows/arm64), producing archives named `fabrica_<version>_<os>_<arch>.<ext>` (ext `tar.gz`, windows `zip`) + `checksums.txt`. Task 2's `install.js` `getArchiveName()` must match this exactly.

- [ ] **Step 1: Write `.goreleaser.yaml`**

Create `.goreleaser.yaml` at the repo root:

```yaml
version: 2

builds:
  - id: fabrica
    main: .
    binary: fabrica
    env:
      - CGO_ENABLED=0
    ldflags:
      - -s -w
      - -X github.com/jpvelasco/fabrica/internal/version.Version={{ .Version }}
      - -X github.com/jpvelasco/fabrica/internal/version.Commit={{ .ShortCommit }}
    goos:
      - linux
      - windows
      - darwin
    goarch:
      - amd64
      - arm64
    ignore:
      - goos: windows
        goarch: arm64

archives:
  - id: fabrica
    formats:
      - tar.gz
    format_overrides:
      - goos: windows
        formats:
          - zip
    name_template: "fabrica_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: checksums.txt

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^ci:"

release:
  # Dormant safety: this block documents that a GitHub Release is created only
  # when GoReleaser runs against a real tag (via release.yml on a v* push).
  # `--snapshot` (used for verification) never publishes or creates a release.
  prerelease: auto
```

Notes:
- Two ldflags entries beyond `-s -w`: both `Version` and `Commit` (Fabrica's version pkg has both; verify the exact var paths against `internal/version/*.go`).
- `name_template` produces `fabrica_<version>_<os>_<arch>`; the archive extension (`tar.gz`/`zip`) is appended by GoReleaser from `formats`. This yields e.g. `fabrica_0.1.0_linux_amd64.tar.gz`, `fabrica_0.1.0_windows_amd64.zip` — must equal Task 2's `getArchiveName()`.

- [ ] **Step 2: Install goreleaser + validate the config**

goreleaser is not installed locally; install it (it's a Go tool):

```bash
go install github.com/goreleaser/goreleaser/v2@latest
export PATH="$PATH:$(go env GOPATH)/bin"
goreleaser --version
goreleaser check
```
Expected: `goreleaser check` reports the config is valid (`1 configuration file(s) validated` or similar). Fix any schema error it reports.

- [ ] **Step 3: Snapshot build (proves it builds all platforms, publishes NOTHING)**

Run: `goreleaser release --snapshot --clean`
Expected: builds succeed for all 5 targets (linux amd64/arm64, darwin amd64/arm64, windows amd64), writing archives + `checksums.txt` into `dist/`. `--snapshot` explicitly does NOT tag, push, or publish. Confirm the produced archive names match the pattern `fabrica_<version>_<os>_<arch>.(tar.gz|zip)`:

```bash
ls dist/*.tar.gz dist/*.zip dist/checksums.txt
```
Expected: 4 `.tar.gz` (linux×2, darwin×2), 1 `.zip` (windows amd64), 1 `checksums.txt`. (Snapshot version string will look like `0.0.0-SNAPSHOT-<sha>` — that's expected for `--snapshot`.)

- [ ] **Step 4: Ensure `dist/` is git-ignored**

Check `.gitignore` has `dist/` (GoReleaser output must not be committed). If absent, add it:

```bash
grep -qxF 'dist/' .gitignore || echo 'dist/' >> .gitignore
```
Then `git status` — confirm `dist/` is not staged/tracked.

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml .gitignore
git commit -m "build: add GoReleaser config (cross-platform binaries + checksums)"
```

---

### Task 2: npm shim package

**Files:**
- Create: `npm/package.json`, `npm/run.js`, `npm/install.js`, `npm/install.test.js`, `npm/.npmignore`, `npm/README.md`

**Interfaces:**
- Consumes: the GitHub Release archives Task 1 produces (`fabrica_<version>_<os>_<arch>.<ext>`).
- Produces: an npm package whose `bin.fabrica` → `run.js`; `install.js` exports `{ ensureBinary, needsDownload, getArchiveName, getPackageVersion, binaryName, MARKER }`; `getArchiveName(version, platform, arch)` returns `fabrica_<version>_<os>_<arch>.<ext>` matching Task 1. Task 3's `embed-checksums.js` writes `binaryChecksums` into this `package.json`.

- [ ] **Step 1: `npm/package.json`**

```json
{
  "name": "fabrica-cli",
  "version": "0.0.0",
  "description": "Studio infrastructure-as-code CLI for AWS — Perforce, Unreal Horde, CI/CD, GameLift deploy, cloud workstations",
  "license": "MIT",
  "repository": {
    "type": "git",
    "url": "https://github.com/jpvelasco/fabrica"
  },
  "bin": {
    "fabrica": "run.js"
  },
  "scripts": {
    "postinstall": "node install.js",
    "test": "node --test"
  },
  "os": [
    "linux",
    "darwin",
    "win32"
  ],
  "cpu": [
    "x64",
    "arm64"
  ],
  "keywords": [
    "aws",
    "infrastructure-as-code",
    "gamedev",
    "perforce",
    "unreal-horde",
    "gamelift",
    "cloud-workstation",
    "ci-cd",
    "iac",
    "cli",
    "devops",
    "cloud-control"
  ],
  "files": [
    "install.js",
    "run.js",
    "bin/"
  ],
  "binaryChecksums": {}
}
```

NOTE (spec-mandated): `"name": "fabrica-cli"` is a PLACEHOLDER — the final npm name (`fabrica-cli` vs scoped `@jpvelasco/fabrica`) is a release-day decision per `docs/superpowers/specs/2026-07-04-release-prep-design.md`. `"version": "0.0.0"` is intentional; the release workflow sets the real version via `npm version` at tag time.

- [ ] **Step 2: `npm/install.js`**

Port of Ludus's, adapted ludus→fabrica. Full content:

```javascript
#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const https = require("https");
const crypto = require("crypto");
const { spawnSync } = require("child_process");

const REPO = "jpvelasco/fabrica";
const MAX_REDIRECTS = 5;
const MARKER = ".installed-version";

const PLATFORM_MAP = {
  linux: "linux",
  darwin: "darwin",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

// log writes routine progress to stderr (never stdout) so it can't corrupt
// `--json` output, and stays quiet when silent.
function log(silent, msg) {
  if (!silent) {
    console.error(msg);
  }
}

function binaryName(platform = process.platform) {
  return platform === "win32" ? "fabrica.exe" : "fabrica";
}

function getPackageVersion() {
  const pkg = JSON.parse(
    fs.readFileSync(path.join(__dirname, "package.json"), "utf8")
  );
  const version = pkg.version;

  // Validate semver format to prevent URL injection
  if (!/^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?(\+[a-zA-Z0-9.]+)?$/.test(version)) {
    throw new Error(`Invalid version format: ${version}`);
  }

  return version;
}

function getExpectedChecksum(archiveName) {
  const pkg = JSON.parse(
    fs.readFileSync(path.join(__dirname, "package.json"), "utf8")
  );
  return pkg.binaryChecksums?.[archiveName] || null;
}

function getArchiveName(version, platform, arch) {
  const os = PLATFORM_MAP[platform];
  const cpu = ARCH_MAP[arch];
  if (!os || !cpu) {
    throw new Error(`Unsupported platform: ${platform}/${arch}`);
  }
  const ext = platform === "win32" ? "zip" : "tar.gz";
  return `fabrica_${version}_${os}_${cpu}.${ext}`;
}

// needsDownload reports whether the binary must be (re)fetched: true when the
// binary is missing, or when the recorded marker version doesn't match the
// package version (drift after a skipped/failed install or an upgrade).
function needsDownload(binDir, version) {
  if (!fs.existsSync(path.join(binDir, binaryName()))) {
    return true;
  }
  try {
    const installed = fs.readFileSync(path.join(binDir, MARKER), "utf8").trim();
    return installed !== version;
  } catch {
    return true;
  }
}

function download(url, redirectCount = 0) {
  return new Promise((resolve, reject) => {
    if (redirectCount > MAX_REDIRECTS) {
      return reject(new Error(`Too many redirects (max ${MAX_REDIRECTS})`));
    }

    https
      .get(url, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          return download(res.headers.location, redirectCount + 1).then(resolve, reject);
        }
        if (res.statusCode !== 200) {
          return reject(new Error(`Download failed: HTTP ${res.statusCode} for ${url}`));
        }
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      })
      .on("error", reject);
  });
}

function verifyChecksum(buffer, archiveName, { silent = false } = {}) {
  const expected = getExpectedChecksum(archiveName);
  if (!expected) {
    log(silent, "fabrica-cli: no checksum available, skipping verification");
    return;
  }

  const actual = crypto.createHash("sha256").update(buffer).digest("hex");
  if (actual !== expected) {
    throw new Error(
      `Checksum mismatch for ${archiveName}\n` +
        `  Expected: ${expected}\n` +
        `  Actual:   ${actual}`
    );
  }
  log(silent, "fabrica-cli: checksum verified (SHA-256)");
}

// Escape a string for use inside PowerShell single quotes.
function psEscape(s) {
  return s.replace(/'/g, "''");
}

function spawnOrFail(cmd, args, label) {
  const result = spawnSync(cmd, args, { stdio: "pipe" });
  if (result.error) {
    throw new Error(`${label}: ${result.error.message}`);
  }
  if (result.status !== 0) {
    const stderr = result.stderr ? result.stderr.toString().trim() : "";
    throw new Error(`${label} exited with code ${result.status}${stderr ? ": " + stderr : ""}`);
  }
}

// placeBinary moves src onto dest atomically. renameSync is atomic on POSIX and
// overwrites; on Windows it refuses to overwrite an existing file, so fall back
// to removing dest first (the binary is never running during a self-heal).
function placeBinary(src, dest) {
  try {
    fs.renameSync(src, dest);
  } catch (err) {
    if (err.code === "EEXIST" || err.code === "EPERM") {
      fs.rmSync(dest, { force: true });
      fs.renameSync(src, dest);
    } else {
      throw err;
    }
  }
}

function extract(buffer, archiveName, binDir) {
  const tmpDir = fs.mkdtempSync(path.join(__dirname, ".tmp-install-"));

  const archivePath = path.join(tmpDir, archiveName);
  fs.writeFileSync(archivePath, buffer);

  try {
    if (archiveName.endsWith(".zip")) {
      if (process.platform === "win32") {
        spawnOrFail(
          "powershell",
          [
            "-NoProfile",
            "-Command",
            `Expand-Archive -Force -Path '${psEscape(archivePath)}' -DestinationPath '${psEscape(tmpDir)}'`,
          ],
          "Expand-Archive"
        );
      } else {
        spawnOrFail("unzip", ["-o", archivePath, "-d", tmpDir], "unzip");
      }
    } else {
      spawnOrFail("tar", ["-xzf", archivePath, "-C", tmpDir], "tar");
    }

    const bn = binaryName();
    const extractedBinary = path.join(tmpDir, bn);

    if (!fs.existsSync(extractedBinary)) {
      throw new Error(`Binary ${bn} not found in archive`);
    }

    if (process.platform !== "win32") {
      fs.chmodSync(extractedBinary, 0o755);
    }

    fs.mkdirSync(binDir, { recursive: true });
    placeBinary(extractedBinary, path.join(binDir, bn));
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

// ensureBinary makes the platform binary present and matching the package
// version, downloading it if missing or stale. No-op when already in sync.
async function ensureBinary({ silent = false } = {}) {
  const version = getPackageVersion();
  if (version === "0.0.0") {
    log(silent, "fabrica-cli: skipping binary download for development version");
    return;
  }

  const binDir = path.join(__dirname, "bin");
  if (!needsDownload(binDir, version)) {
    return;
  }

  const archiveName = getArchiveName(version, process.platform, process.arch);
  const url = `https://github.com/${REPO}/releases/download/v${version}/${archiveName}`;

  console.error(`fabrica-cli: fetching fabrica binary v${version}...`);

  log(silent, `fabrica-cli: downloading ${archiveName}...`);
  const buffer = await download(url);

  verifyChecksum(buffer, archiveName, { silent });

  log(silent, "fabrica-cli: extracting binary...");
  extract(buffer, archiveName, binDir);

  fs.writeFileSync(path.join(binDir, MARKER), version);

  log(silent, "fabrica-cli: installed successfully");
}

if (require.main === module) {
  ensureBinary({ silent: false }).catch((err) => {
    console.error(`fabrica-cli: installation failed: ${err.message}`);
    process.exit(1);
  });
}

module.exports = {
  ensureBinary,
  needsDownload,
  getArchiveName,
  getPackageVersion,
  binaryName,
  MARKER,
};
```

- [ ] **Step 3: `npm/run.js`**

```javascript
#!/usr/bin/env node
"use strict";

const path = require("path");
const { spawn } = require("child_process");
const { ensureBinary, binaryName } = require("./install.js");

const binaryPath = path.join(__dirname, "bin", binaryName());

function spawnBinary() {
  const child = spawn(binaryPath, process.argv.slice(2), {
    stdio: "inherit",
  });

  ["SIGINT", "SIGTERM", "SIGHUP"].forEach((sig) => {
    process.on(sig, () => child.kill(sig));
  });

  child.on("error", (err) => {
    if (err.code === "ENOENT") {
      console.error(
        `fabrica-cli: binary not found at ${binaryPath}\n` +
          "Reinstall with: npm install -g fabrica-cli@latest"
      );
    } else {
      console.error(`fabrica-cli: failed to start: ${err.message}`);
    }
    process.exit(1);
  });

  child.on("exit", (code, signal) => {
    process.exit(signal ? 1 : code || 0);
  });
}

async function main() {
  if (process.env.FABRICA_SKIP_AUTO_DOWNLOAD) {
    spawnBinary();
    return;
  }

  try {
    await ensureBinary({ silent: true });
  } catch (err) {
    const code = err && err.code;
    if (code === "EACCES" || code === "EPERM") {
      console.error(
        `fabrica-cli: cannot write the fabrica binary (permission denied).\n` +
          "Reinstall with appropriate privileges:\n" +
          "  sudo npm install -g fabrica-cli@latest    (macOS/Linux)\n" +
          "  run your shell as Administrator, then the same command (Windows)"
      );
    } else {
      console.error(
        `fabrica-cli: could not fetch the fabrica binary: ${err.message}\n` +
          "Check your network/proxy and retry. If you manage the binary yourself,\n" +
          "set FABRICA_SKIP_AUTO_DOWNLOAD=1 to bypass this step."
      );
    }
    process.exit(1);
  }

  spawnBinary();
}

main();
```

- [ ] **Step 4: `npm/install.test.js`**

Port of Ludus's tests, adapted (archive names → `fabrica_...`):

```javascript
"use strict";

const { test } = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const {
  getArchiveName,
  needsDownload,
  binaryName,
  MARKER,
} = require("./install.js");

test("getArchiveName: platform/arch matrix", () => {
  assert.strictEqual(
    getArchiveName("1.2.3", "win32", "x64"),
    "fabrica_1.2.3_windows_amd64.zip"
  );
  assert.strictEqual(
    getArchiveName("1.2.3", "linux", "arm64"),
    "fabrica_1.2.3_linux_arm64.tar.gz"
  );
  assert.strictEqual(
    getArchiveName("0.5.1", "darwin", "arm64"),
    "fabrica_0.5.1_darwin_arm64.tar.gz"
  );
  assert.strictEqual(
    getArchiveName("0.5.1", "darwin", "x64"),
    "fabrica_0.5.1_darwin_amd64.tar.gz"
  );
});

test("getArchiveName: unsupported platform/arch throws", () => {
  assert.throws(() => getArchiveName("1.0.0", "sunos", "x64"), /Unsupported platform/);
  assert.throws(() => getArchiveName("1.0.0", "linux", "mips"), /Unsupported platform/);
});

const VERSION_RE = /^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?(\+[a-zA-Z0-9.]+)?$/;

test("version regex: accepts valid semver", () => {
  for (const v of ["0.0.0", "1.2.3", "0.5.1", "1.0.0-rc.1", "1.2.3+build.5"]) {
    assert.ok(VERSION_RE.test(v), `expected ${v} to be valid`);
  }
});

test("version regex: rejects injection-y / malformed strings", () => {
  for (const v of [
    "1.2.3; rm -rf /",
    "../../etc/passwd",
    "v1.2.3",
    "1.2",
    "latest",
    "1.2.3 && echo hi",
  ]) {
    assert.ok(!VERSION_RE.test(v), `expected ${v} to be rejected`);
  }
});

test("needsDownload: true when binary missing", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fabrica-test-"));
  try {
    assert.strictEqual(needsDownload(dir, "1.2.3"), true);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("needsDownload: true when marker missing", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fabrica-test-"));
  try {
    fs.writeFileSync(path.join(dir, binaryName()), "stub");
    assert.strictEqual(needsDownload(dir, "1.2.3"), true);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("needsDownload: true when marker version mismatches (drift)", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fabrica-test-"));
  try {
    fs.writeFileSync(path.join(dir, binaryName()), "stub");
    fs.writeFileSync(path.join(dir, MARKER), "1.2.2");
    assert.strictEqual(needsDownload(dir, "1.2.3"), true);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("needsDownload: false when binary present and marker matches", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fabrica-test-"));
  try {
    fs.writeFileSync(path.join(dir, binaryName()), "stub");
    fs.writeFileSync(path.join(dir, MARKER), "1.2.3");
    assert.strictEqual(needsDownload(dir, "1.2.3"), false);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("needsDownload: tolerates trailing whitespace in marker", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fabrica-test-"));
  try {
    fs.writeFileSync(path.join(dir, binaryName()), "stub");
    fs.writeFileSync(path.join(dir, MARKER), "1.2.3\n");
    assert.strictEqual(needsDownload(dir, "1.2.3"), false);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
```

- [ ] **Step 5: `npm/.npmignore`**

```
bin/
.tmp-install/
```

- [ ] **Step 6: `npm/README.md`**

Create a short npm-facing readme:

```markdown
# fabrica-cli

npm distribution of [Fabrica](https://github.com/jpvelasco/fabrica) — a Go CLI + infrastructure-as-code framework that provisions and manages game-studio cloud infrastructure on AWS (Perforce Helix Core, Unreal Horde, CI/CD, GameLift deployment, cloud workstations).

## Install

```bash
npm install -g fabrica-cli
# or run without installing:
npx fabrica-cli --help
```

This package is a thin launcher: on install it downloads the matching prebuilt `fabrica` binary for your platform from the [GitHub Releases](https://github.com/jpvelasco/fabrica/releases) and verifies its SHA-256 checksum. Supported: linux/macOS/windows on amd64, and linux/macOS on arm64.

Set `FABRICA_SKIP_AUTO_DOWNLOAD=1` to manage the binary yourself (air-gapped setups).

## Alternative install

```bash
go install github.com/jpvelasco/fabrica@latest
```

See the [main README](https://github.com/jpvelasco/fabrica#readme) for usage.
```

(If the package name changes at release day, update the `fabrica-cli` references here too.)

- [ ] **Step 7: Run the shim's tests + verify archive-name match**

```bash
cd npm && node --test && cd ..
```
Expected: all `install.test.js` tests PASS.

Then confirm `getArchiveName()` matches Task 1's GoReleaser output. If Task 1's snapshot `dist/` still exists:
```bash
node -e "const {getArchiveName}=require('./npm/install.js'); console.log(getArchiveName('0.1.0','linux','x64'), getArchiveName('0.1.0','win32','x64'))"
# expect: fabrica_0.1.0_linux_amd64.tar.gz fabrica_0.1.0_windows_amd64.zip
ls dist/ 2>/dev/null | grep -E "fabrica_.*_(linux_amd64\.tar\.gz|windows_amd64\.zip)" || echo "(snapshot dist not present — names verified by pattern above)"
```
Expected: the `node -e` output matches the `fabrica_<version>_<os>_<arch>.<ext>` names GoReleaser produces.

- [ ] **Step 8: Commit**

```bash
git add npm/
git commit -m "build: add npm shim package (downloads + verifies platform binary; ludus-cli pattern)"
```

---

### Task 3: Checksum embedder + release workflow + CI snapshot validation

**Files:**
- Create: `scripts/embed-checksums.js`, `.github/workflows/release.yml`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Task 1's GoReleaser config (produces `dist/checksums.txt`), Task 2's `npm/package.json` (`binaryChecksums` field), `npm/install.test.js`.
- Produces: `scripts/embed-checksums.js` (CLI: `node scripts/embed-checksums.js <checksums.txt> <package.json>`); a dormant `release.yml` (fires only on `push: tags: v*`); a CI `goreleaser` job that snapshot-builds + runs the npm tests on every push/PR.

- [ ] **Step 1: `scripts/embed-checksums.js`**

Port of Ludus's, verbatim (it's repo-agnostic):

```javascript
#!/usr/bin/env node
"use strict";

// embed-checksums.js — Embeds SHA-256 checksums from GoReleaser's checksums.txt
// into npm/package.json's binaryChecksums field.
//
// Usage: node scripts/embed-checksums.js dist/checksums.txt npm/package.json
//
// GoReleaser checksums.txt format:
//   <sha256hex>  <filename>

const fs = require("fs");

const [checksumFile, packageFile] = process.argv.slice(2);
if (!checksumFile || !packageFile) {
  console.error("Usage: node embed-checksums.js <checksums.txt> <package.json>");
  process.exit(1);
}

const checksums = fs.readFileSync(checksumFile, "utf8");
const pkg = JSON.parse(fs.readFileSync(packageFile, "utf8"));

pkg.binaryChecksums = {};
for (const line of checksums.split("\n")) {
  const match = line.match(/^([0-9a-f]{64})\s+(.+)$/);
  if (match) {
    const [, hash, filename] = match;
    pkg.binaryChecksums[filename] = hash;
  }
}

const count = Object.keys(pkg.binaryChecksums).length;
if (count === 0) {
  console.error("Warning: no checksums found in " + checksumFile);
  process.exit(1);
}

fs.writeFileSync(packageFile, JSON.stringify(pkg, null, 2) + "\n");
console.log(`Embedded ${count} checksums into ${packageFile}`);
```

- [ ] **Step 2: Test embed-checksums against a real snapshot (populates a COPY, not the tracked file)**

If Task 1's `dist/checksums.txt` exists (re-run `goreleaser release --snapshot --clean` if not, with goreleaser on PATH), verify the embedder works without dirtying `npm/package.json`:

```bash
cp npm/package.json /tmp/pkg-test.json
node scripts/embed-checksums.js dist/checksums.txt /tmp/pkg-test.json
node -e "const p=require('/tmp/pkg-test.json'); const n=Object.keys(p.binaryChecksums).length; if(n<5){console.error('expected >=5 checksums, got '+n);process.exit(1)} console.log('OK: '+n+' checksums embedded')"
rm /tmp/pkg-test.json
```
Expected: `OK: N checksums embedded` (N ≥ 5: one per archive). The tracked `npm/package.json` stays `binaryChecksums: {}` (only the release workflow populates it, at tag time). Confirm: `git diff npm/package.json` is empty.

- [ ] **Step 3: `.github/workflows/release.yml`**

Port of Ludus's, adapted (embed path, publish dir). This is DORMANT — `on: push: tags: v*` only:

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write   # GoReleaser creates the GitHub Release + uploads assets
  id-token: write   # npm trusted publishing (OIDC) at release day

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0  # v7.0.0
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16  # v6
        with:
          go-version-file: go.mod

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94  # v7
        with:
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

      - name: Set up Node.js
        uses: actions/setup-node@48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e  # v6
        with:
          node-version: "24"
          registry-url: "https://registry.npmjs.org"

      - name: Embed binary checksums into npm package
        run: node scripts/embed-checksums.js dist/checksums.txt npm/package.json

      - name: Publish npm package
        working-directory: npm
        # npm auth at release day: OIDC trusted publisher (preferred; id-token
        # is granted above) OR a NODE_AUTH_TOKEN/NPM_TOKEN secret. If using a
        # token, add `env: NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}` here. Do
        # NOT add a secret now — this workflow is dormant until a v* tag.
        run: |
          VERSION="${GITHUB_REF_NAME#v}"
          npm version "$VERSION" --no-git-tag-version
          npm publish --access public
```

- [ ] **Step 4: Add a snapshot-validation job to `.github/workflows/ci.yml`**

Append a new `goreleaser` job (build-only, never publishes) after the existing `test:` job in `ci.yml`. It also runs the npm shim's tests so config/shim breakage is caught at PR time:

```yaml
  goreleaser:
    name: Release build (snapshot)
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0  # v7.0.0
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16  # v6
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@48b55a011bda9f5d6aeb4c2d9c7362e8dae4041e  # v6
        with:
          node-version: "24"
      # Build-only: verifies .goreleaser.yaml compiles all platforms. NEVER
      # publishes (no `release`, no tag). Catches config breakage on PRs.
      - uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94  # v7
        with:
          args: build --snapshot --clean
      - name: npm shim tests
        working-directory: npm
        run: node --test
```

- [ ] **Step 5: Validate the workflow YAML + confirm dormancy**

```bash
python -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); yaml.safe_load(open('.github/workflows/ci.yml')); print('workflows valid')"
grep -A3 "^on:" .github/workflows/release.yml   # expect: push: tags: v* ONLY (no branches, no workflow_dispatch)
```
Expected: both parse; `release.yml` triggers only on `v*` tags (dormant). Confirm there is NO `git tag` anywhere and no `workflow_dispatch` that could fire it manually.

- [ ] **Step 6: Commit**

```bash
git add scripts/embed-checksums.js .github/workflows/release.yml .github/workflows/ci.yml
git commit -m "build: dormant tag-triggered release workflow + checksum embedder + CI snapshot validation"
```

---

### Task 4: CHANGELOG + docs + final verification

**Files:**
- Create: `CHANGELOG.md`
- Modify: `README.md`, `ROADMAP.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the shipped module surface (from ROADMAP/CLAUDE).
- Produces: release-facing docs. No code interface.

- [ ] **Step 1: `CHANGELOG.md` (Keep a Changelog, `[Unreleased]`)**

Create `CHANGELOG.md` at the repo root:

```markdown
# Changelog

All notable changes to Fabrica are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first tagged release will cover the Phase 1 core: the foundation plus six
provisioning/management modules, full-stack teardown, and cost visibility.

### Added

- **Foundation:** `fabrica setup` (S3 + DynamoDB state backend, idempotent),
  `fabrica status` (aggregate read-only health across modules, `--probe`),
  `fabrica doctor` (prerequisite validation), `fabrica config show`.
- **Perforce module:** `perforce create` / `status` / `destroy` — provisions
  Perforce Helix Core on EC2.
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

[Unreleased]: https://github.com/jpvelasco/fabrica/commits/main
```

No version number or date — those are filled when the tag is cut (release day).

- [ ] **Step 2: README.md — add an Install section**

In `README.md`, add an `## Install` section (place it just before or after the existing "Building" section — match surrounding structure). Content:

```markdown
## Install

Fabrica ships as a single Go binary. Two ways to get it:

```bash
# Via npm (downloads the matching prebuilt binary for your platform):
npm install -g fabrica-cli
# …or run without installing:
npx fabrica-cli --help

# Or via the Go toolchain:
go install github.com/jpvelasco/fabrica@latest
```

Prebuilt binaries for linux/macOS/windows (amd64) and linux/macOS (arm64) are
attached to each [GitHub Release](https://github.com/jpvelasco/fabrica/releases).
```

(The npm package name `fabrica-cli` is provisional — confirmed at first release.)

- [ ] **Step 3: ROADMAP.md — mark M5-E**

In `ROADMAP.md`, find the Milestone 5 line `- ⬜ v0.1 / v1.0 release preparation` and replace with:

```markdown
- ✅ Release machinery — GoReleaser + npm shim (ludus-cli pattern), dormant until a `v*` tag; no release cut yet
```

- [ ] **Step 4: CLAUDE.md — add a "Releasing" section**

In `CLAUDE.md`, add a short section (after the "Git Hooks" section is a sensible spot):

```markdown
## Releasing

Distribution follows the Ludus pattern: GoReleaser builds cross-platform
binaries + a GitHub Release; the `npm/` shim downloads the matching binary at
install time. The pipeline is **dormant** — `.github/workflows/release.yml`
fires only on a `v*` tag push. CI validates the config on every PR via a
`goreleaser build --snapshot` job (build-only, never publishes).

**Cutting a release (deliberate, not automated):**
1. Decide/confirm the npm package name in `npm/package.json`.
2. Set up the npm org + trusted publisher (OIDC) — one-time, see the npm-init flow.
3. Move `CHANGELOG.md` `[Unreleased]` → `[X.Y.Z]` with the date.
4. `git tag vX.Y.Z && git push origin vX.Y.Z` — this triggers `release.yml`
   (GoReleaser → embed checksums → npm publish). Nothing publishes without a tag.
```

- [ ] **Step 5: Full verification gate**

```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
golangci-lint run ./...
cd npm && node --test && cd ..
python -c "import json; json.load(open('npm/package.json')); print('package.json valid')"
git tag   # expect: EMPTY — no tag created by any of this work
```
Expected: build clean, all Go tests pass, vet clean, `gofmt -l` empty, golangci-lint 0 issues, npm tests pass, package.json valid, **`git tag` prints nothing** (the release guardrail).

- [ ] **Step 6: Commit**

```bash
git add CHANGELOG.md README.md ROADMAP.md CLAUDE.md
git commit -m "docs: CHANGELOG (Unreleased) + install/releasing docs (Milestone 5-E)"
```

## Plan complete

Spec coverage:
- `.goreleaser.yaml` (binaries + checksums, version+commit ldflags) → Task 1.
- npm shim (package.json placeholder name, run.js, install.js matching archive names, tests, .npmignore, README) → Task 2.
- `embed-checksums.js` + dormant `release.yml` (tag-only, GITHUB_TOKEN, OIDC-ready npm publish) + CI snapshot-validation job → Task 3.
- CHANGELOG (Unreleased, no version/date) + README install + ROADMAP + CLAUDE "Releasing" → Task 4.
- Verification is snapshot-only; final gate asserts `git tag` is empty (no release cut).
- Release-day prerequisites (npm name, org/trusted-publisher, tag) documented in CLAUDE.md + spec, NOT done.
- Out-of-scope (actual release, Lore, homebrew) untouched.
