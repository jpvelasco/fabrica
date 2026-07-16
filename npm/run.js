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
