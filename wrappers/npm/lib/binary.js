"use strict";

// Node (platform, arch) pairs with a published platform package; mirrors
// the goreleaser build matrix. Alphabetical.
const SUPPORTED = new Set([
  "darwin-arm64",
  "darwin-x64",
  "linux-arm64",
  "linux-x64",
  "win32-arm64",
  "win32-x64",
]);

// binaryPath resolves the agentminutes binary: the AGENTMINUTES_BINARY
// override first, then the platform package installed via
// optionalDependencies.
function binaryPath() {
  const override = process.env.AGENTMINUTES_BINARY;
  if (override) return override;
  const key = `${process.platform}-${process.arch}`;
  if (!SUPPORTED.has(key)) {
    throw new Error(
      `agentminutes: no prebuilt binary for ${key}; install the Go CLI instead ` +
        "(https://github.com/agent-ecosystem/agentminutes) and set AGENTMINUTES_BINARY",
    );
  }
  const exe = process.platform === "win32" ? "agentminutes.exe" : "agentminutes";
  try {
    return require.resolve(`agentminutes-${key}/bin/${exe}`);
  } catch {
    throw new Error(
      `agentminutes: platform package agentminutes-${key} is missing; it installs ` +
        "automatically as an optional dependency, so reinstall with optional " +
        "dependencies enabled, or set AGENTMINUTES_BINARY to a binary you provide",
    );
  }
}

module.exports = { binaryPath };
