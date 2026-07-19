#!/usr/bin/env node
// Assembles the publishable npm packages for one release: six platform
// packages (binary inside, os/cpu-gated) plus the version-stamped main
// package. Input is the goreleaser release archives; run from anywhere:
//
//   node build-packages.mjs --version 0.1.1 --assets <dir> --out <dir>
//
// Publish the platform packages before the main package so the exact-
// version optionalDependencies never dangle.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Node platform key → goreleaser artifact identity. Mirrors SUPPORTED in
// lib/binary.js; both move together with .goreleaser.yaml's build matrix.
const PLATFORMS = [
  { key: "darwin-arm64", os: "darwin", cpu: "arm64", artifact: "darwin_arm64", ext: "tar.gz" },
  { key: "darwin-x64", os: "darwin", cpu: "x64", artifact: "darwin_amd64", ext: "tar.gz" },
  { key: "linux-arm64", os: "linux", cpu: "arm64", artifact: "linux_arm64", ext: "tar.gz" },
  { key: "linux-x64", os: "linux", cpu: "x64", artifact: "linux_amd64", ext: "tar.gz" },
  { key: "win32-arm64", os: "win32", cpu: "arm64", artifact: "windows_arm64", ext: "zip" },
  { key: "win32-x64", os: "win32", cpu: "x64", artifact: "windows_amd64", ext: "zip" },
];

function arg(name) {
  const i = process.argv.indexOf(`--${name}`);
  if (i === -1 || i + 1 >= process.argv.length) {
    console.error(`build-packages: --${name} is required`);
    process.exit(64);
  }
  return process.argv[i + 1];
}

const version = arg("version").replace(/^v/, "");
const assets = path.resolve(arg("assets"));
const out = path.resolve(arg("out"));
const wrapperDir = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

const mainPkg = JSON.parse(fs.readFileSync(path.join(wrapperDir, "package.json"), "utf8"));
// Repo-root LICENSE rides along in every package; npm includes LICENSE
// files regardless of the "files" allowlist.
const licensePath = path.join(wrapperDir, "..", "..", "LICENSE");

fs.rmSync(out, { recursive: true, force: true });

for (const p of PLATFORMS) {
  const archive = path.join(assets, `agentminutes_${version}_${p.artifact}.${p.ext}`);
  if (!fs.existsSync(archive)) {
    console.error(`build-packages: missing release archive ${archive}`);
    process.exit(66);
  }
  const pkgDir = path.join(out, "platform", `agentminutes-${p.key}`);
  const binDir = path.join(pkgDir, "bin");
  fs.mkdirSync(binDir, { recursive: true });
  const exe = p.os === "win32" ? "agentminutes.exe" : "agentminutes";
  if (p.ext === "zip") {
    execFileSync("unzip", ["-o", "-q", archive, exe, "-d", binDir]);
  } else {
    execFileSync("tar", ["-xzf", archive, "-C", binDir, exe]);
  }
  fs.chmodSync(path.join(binDir, exe), 0o755);
  fs.writeFileSync(
    path.join(pkgDir, "package.json"),
    JSON.stringify(
      {
        name: `agentminutes-${p.key}`,
        version,
        description: `agentminutes binary for ${p.key}. Install the "agentminutes" package instead.`,
        os: [p.os],
        cpu: [p.cpu],
        files: ["bin"],
        author: mainPkg.author,
        license: mainPkg.license,
        homepage: mainPkg.homepage,
        repository: mainPkg.repository,
      },
      null,
      2,
    ) + "\n",
  );
  fs.writeFileSync(
    path.join(pkgDir, "README.md"),
    `# agentminutes-${p.key}\n\nPlatform binary for [agentminutes](https://www.npmjs.com/package/agentminutes); installed automatically as an optional dependency. Install \`agentminutes\` itself, not this package.\n`,
  );
  fs.copyFileSync(licensePath, path.join(pkgDir, "LICENSE"));
}

// Main package: the checked-in sources, with version and the exact-version
// optionalDependencies stamped.
const mainDir = path.join(out, "agentminutes");
fs.mkdirSync(mainDir, { recursive: true });
for (const entry of ["bin", "lib", "README.md"]) {
  fs.cpSync(path.join(wrapperDir, entry), path.join(mainDir, entry), { recursive: true });
}
fs.copyFileSync(licensePath, path.join(mainDir, "LICENSE"));
mainPkg.version = version;
delete mainPkg.scripts;
for (const dep of Object.keys(mainPkg.optionalDependencies)) {
  mainPkg.optionalDependencies[dep] = version;
}
fs.writeFileSync(path.join(mainDir, "package.json"), JSON.stringify(mainPkg, null, 2) + "\n");

console.log(`build-packages: wrote ${PLATFORMS.length} platform packages and the main package to ${out}`);
