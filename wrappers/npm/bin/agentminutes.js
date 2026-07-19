#!/usr/bin/env node
"use strict";

// Transparent passthrough to the agentminutes binary: argv, stdio, and
// exit code all forward untouched, so `npx agentminutes` behaves exactly
// like the Go CLI.
const { spawn } = require("node:child_process");
const { binaryPath } = require("../lib/binary");

let bin;
try {
  bin = binaryPath();
} catch (err) {
  console.error(err.message);
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
// The terminal delivers Ctrl-C to the whole foreground group; staying alive
// until the child reports lets its exit code propagate instead of ours.
process.on("SIGINT", () => {});
process.on("SIGTERM", () => child.kill("SIGTERM"));
child.on("error", (err) => {
  console.error(`agentminutes: ${err.message}`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
