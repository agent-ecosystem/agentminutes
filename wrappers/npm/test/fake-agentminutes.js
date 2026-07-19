#!/usr/bin/env node
"use strict";

// Fake agentminutes binary for wrapper tests: FAKE_* env vars drive the
// output, and FAKE_ECHO_ARGS prints received argv so tests can assert the
// shim forwards arguments untouched.
if (process.env.FAKE_ECHO_ARGS === "1") {
  process.stdout.write(JSON.stringify(process.argv.slice(2)));
  process.exit(0);
}

process.stdout.write(process.env.FAKE_STDOUT || "");
process.stderr.write(process.env.FAKE_STDERR || "");
process.exit(Number(process.env.FAKE_EXIT || 0));
