#!/usr/bin/env node
"use strict";

// embed-checksums.js - Embeds SHA-256 checksums from GoReleaser's checksums.txt
// into npm/package.json's binaryChecksums field.
//
// Usage: node scripts/embed-checksums.js dist/checksums.txt npm/package.json
//
// GoReleaser checksums.txt format:
//   <sha256hex>  <filename>

const fs = require("fs");
const path = require("path");

// resolveWithin validates that resolved stays under baseDir — prevents path traversal.
// Uses path-separator boundary check to avoid sibling-prefix escapes
// (e.g. /workspace/fabrica matching /workspace/fabrica-evil).
function resolveWithin(baseDir, subPath) {
  // nosemgrep: path.resolve is the sanitization step — result is validated below
  const resolved = path.resolve(baseDir, subPath);
  const normalized = path.resolve(baseDir);
  if (
    resolved !== normalized &&
    !resolved.startsWith(normalized + path.sep)
  ) {
    throw new Error(`Path escapes allowed directory: ${subPath}`);
  }
  return resolved;
}

// validateFile ensures the path is within the project root and the file exists.
// nosemgrep: path validated by resolveWithin below
function validateFile(baseDir, filePath, label) {
  const resolved = resolveWithin(baseDir, filePath);
  if (!fs.existsSync(resolved)) {
    throw new Error(`${label} not found: ${resolved}`);
  }
  return resolved;
}

const scriptDir = __dirname;
const projectRoot = path.resolve(scriptDir, "..");

const [checksumFile, packageFile] = process.argv.slice(2);
if (!checksumFile || !packageFile) {
  console.error("Usage: node embed-checksums.js <checksums.txt> <package.json>");
  process.exit(1);
}

// Validate both paths are within the project root.
// nosemgrep: paths validated by validateFile (resolveWithin + exists check)
const checksumPath = validateFile(projectRoot, checksumFile, "Checksum file");
const packagePath = validateFile(projectRoot, packageFile, "Package file");

// Validate input file extensions to prevent accidental misuse.
if (!checksumPath.endsWith(".txt")) {
  console.error(`Checksum file must be a .txt file: ${checksumPath}`);
  process.exit(1);
}
if (!packagePath.endsWith("package.json")) {
  console.error(`Package file must be package.json: ${packagePath}`);
  process.exit(1);
}

const checksums = fs.readFileSync(checksumPath, "utf8");
const pkg = JSON.parse(fs.readFileSync(packagePath, "utf8"));

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
  console.error("Warning: no checksums found in " + checksumPath);
  process.exit(1);
}

// nosemgrep: packagePath validated by validateFile above
fs.writeFileSync(packagePath, JSON.stringify(pkg, null, 2) + "\n");
console.log(`Embedded ${count} checksums into ${packagePath}`);