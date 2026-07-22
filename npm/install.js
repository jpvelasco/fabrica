#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const https = require("https");
const crypto = require("crypto");
const { spawnSync } = require("child_process");

// Whitelist of allowed commands for spawnOrFail — prevents arbitrary command execution.
const ALLOWED_COMMANDS = new Set(["powershell", "unzip", "tar"]);

const REPO = "jpvelasco/fabrica";
const MAX_REDIRECTS = 5;
const MARKER = ".installed-version";

// Only allow downloads from the expected GitHub releases host.
const GITHUB_RELEASES_HOST = "github.com";
const GITHUB_REDIRECT_HOSTS = new Set(["github.com", "objects.githubusercontent.com", "github-production-release-asset.githubusercontent.com"]);

const PLATFORM_MAP = {
  linux: "linux",
  darwin: "darwin",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

// resolveWithin validates that resolved stays under baseDir — prevents path traversal.
function resolveWithin(baseDir, subPath) {
  const resolved = path.resolve(baseDir, subPath);
  if (!resolved.startsWith(baseDir)) {
    throw new Error(`Path escapes allowed directory: ${subPath}`);
  }
  return resolved;
}

// validateDownloadUrl rejects URLs that do not point to the expected GitHub
// releases host — prevents SSRF / user-controlled URL injection.
function validateDownloadUrl(url) {
  const parsed = new URL(url);
  if (parsed.protocol !== "https:") {
    throw new Error(`Download URL must use HTTPS: ${url}`);
  }
  if (parsed.hostname !== GITHUB_RELEASES_HOST) {
    throw new Error(`Download URL must be from ${GITHUB_RELEASES_HOST}: ${url}`);
  }
  // Ensure it's a releases download URL.
  if (!parsed.pathname.startsWith(`/${REPO}/releases/download/`)) {
    throw new Error(`Download URL must be a release asset: ${url}`);
  }
}

// validateRedirectUrl checks that a redirect target is a trusted GitHub host.
function validateRedirectUrl(url) {
  const parsed = new URL(url);
  if (parsed.protocol !== "https:") {
    throw new Error(`Redirect must use HTTPS: ${url}`);
  }
  if (!GITHUB_REDIRECT_HOSTS.has(parsed.hostname)) {
    throw new Error(`Redirect to untrusted host: ${url}`);
  }
}

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
  // GoReleaser ignores windows/arm64, so no such archive is ever published.
  // npm's os/cpu fields would otherwise allow install here; reject it up front
  // with a 404-free, actionable error rather than a failed download.
  if (os === "windows" && cpu === "arm64") {
    throw new Error(
      "Unsupported platform: windows/arm64 (no prebuilt binary). " +
        "Install from source instead: go install github.com/jpvelasco/fabrica@latest"
    );
  }
  const ext = platform === "win32" ? "zip" : "tar.gz";
  return `fabrica_${version}_${os}_${cpu}.${ext}`;
}

// needsDownload reports whether the binary must be (re)fetched: true when the
// binary is missing, or when the recorded marker version doesn't match the
// package version (drift after a skipped/failed install or an upgrade).
function needsDownload(binDir, version) {
  const binPath = resolveWithin(binDir, binaryName());
  if (!fs.existsSync(binPath)) {
    return true;
  }
  try {
    const markerPath = resolveWithin(binDir, MARKER);
    const installed = fs.readFileSync(markerPath, "utf8").trim();
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

    // Validate the initial URL against the allowlist.
    if (redirectCount === 0) {
      try {
        validateDownloadUrl(url);
      } catch (err) {
        return reject(err);
      }
    }

    https
      .get(url, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          // Validate redirect target before following.
          try {
            validateRedirectUrl(res.headers.location);
          } catch (err) {
            return reject(err);
          }
          return download(res.headers.location, redirectCount + 1).then(resolve, reject);
        }
        if (res.statusCode !== 200) {
          return reject(new Error(`Download failed: HTTP ${res.statusCode}`));
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
  // Whitelist check — only allow known-safe commands.
  if (!ALLOWED_COMMANDS.has(cmd)) {
    throw new Error(`Command not allowed: ${cmd}`);
  }

  const result = spawnSync(cmd, args, { stdio: "pipe", windowsVerbatimArguments: true });
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

  const archivePath = resolveWithin(tmpDir, archiveName);
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
    const extractedBinary = resolveWithin(tmpDir, bn);

    if (!fs.existsSync(extractedBinary)) {
      throw new Error(`Binary ${bn} not found in archive`);
    }

    if (process.platform !== "win32") {
      fs.chmodSync(extractedBinary, 0o755);
    }

    fs.mkdirSync(binDir, { recursive: true });
    placeBinary(extractedBinary, resolveWithin(binDir, bn));
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
  const url = `https://${GITHUB_RELEASES_HOST}/${REPO}/releases/download/v${version}/${archiveName}`;

  console.error(`fabrica-cli: fetching fabrica binary v${version}...`);

  log(silent, `fabrica-cli: downloading ${archiveName}...`);
  const buffer = await download(url);

  verifyChecksum(buffer, archiveName, { silent });

  log(silent, "fabrica-cli: extracting binary...");
  extract(buffer, archiveName, binDir);

  const markerPath = resolveWithin(binDir, MARKER);
  fs.writeFileSync(markerPath, version);

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
  // Exported for testing only.
  resolveWithin,
  validateDownloadUrl,
  validateRedirectUrl,
};