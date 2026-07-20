"use strict";

// Node (platform, arch) pairs with a published platform package; mirrors
// PLATFORMS in scripts/build-packages.mjs. Alphabetical. win32-x64 is
// temporarily absent while npm blocks the package name (see the PLATFORMS
// comment); the goreleaser release still builds that binary.
const SUPPORTED = new Set([
  "darwin-arm64",
  "darwin-x64",
  "linux-arm64",
  "linux-x64",
  "win32-arm64",
]);

// binaryPath resolves the agentminutes binary: the AGENTMINUTES_BINARY
// override first, then the platform package installed via
// optionalDependencies.
function binaryPath() {
  const override = process.env.AGENTMINUTES_BINARY;
  if (override) return override;
  const key = `${process.platform}-${process.arch}`;
  if (!SUPPORTED.has(key)) {
    const hint =
      key === "win32-x64"
        ? "the npm platform package is temporarily unavailable; download the Windows " +
          "binary from https://github.com/agent-ecosystem/agentminutes/releases and set " +
          "AGENTMINUTES_BINARY, or use the PyPI package"
        : "install the Go CLI instead (https://github.com/agent-ecosystem/agentminutes) " +
          "and set AGENTMINUTES_BINARY";
    throw new Error(`agentminutes: no prebuilt binary for ${key}; ${hint}`);
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
