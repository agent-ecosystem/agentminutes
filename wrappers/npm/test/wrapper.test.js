"use strict";

const assert = require("node:assert/strict");
const { execFile } = require("node:child_process");
const path = require("node:path");
const { test } = require("node:test");

const { binaryPath, projectUrl } = require("../lib/index");

const FAKE = path.join(__dirname, "fake-agentminutes.js");
const SHIM = path.join(__dirname, "..", "bin", "agentminutes.js");

function withEnv(env, fn) {
  const saved = {};
  for (const [k, v] of Object.entries(env)) {
    saved[k] = process.env[k];
    if (v === undefined) delete process.env[k];
    else process.env[k] = v;
  }
  const restore = () => {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  };
  try {
    const result = fn();
    if (result && typeof result.finally === "function") return result.finally(restore);
    restore();
    return result;
  } catch (err) {
    restore();
    throw err;
  }
}

test("binaryPath honors AGENTMINUTES_BINARY", () =>
  withEnv({ AGENTMINUTES_BINARY: FAKE }, () => {
    assert.equal(binaryPath(), FAKE);
  }));

test("binaryPath explains a missing platform package", () =>
  withEnv({ AGENTMINUTES_BINARY: undefined }, () => {
    // In the source tree no platform package is installed, so resolution
    // must fail with the reinstall/override guidance, not an opaque error.
    assert.throws(() => binaryPath(), /agentminutes-|no prebuilt binary/);
  }));

test("projectUrl still points at the repository", () => {
  assert.equal(projectUrl(), "https://github.com/agent-ecosystem/agentminutes");
});

test("shim passes stdio through and propagates the exit code", () => {
  return new Promise((resolve, reject) => {
    execFile(
      process.execPath,
      [SHIM, "stats", "session.jsonl"],
      {
        env: {
          ...process.env,
          AGENTMINUTES_BINARY: FAKE,
          FAKE_STDOUT: "out",
          FAKE_STDERR: "err",
          FAKE_EXIT: "3",
        },
      },
      (err, stdout, stderr) => {
        try {
          assert.equal(err && err.code, 3);
          assert.equal(stdout, "out");
          assert.equal(stderr, "err");
          resolve();
        } catch (assertion) {
          reject(assertion);
        }
      },
    );
  });
});

test("shim forwards argv untouched", () => {
  return new Promise((resolve, reject) => {
    const args = ["convert", "--format", "jsonl", "--", "weird name.jsonl"];
    execFile(
      process.execPath,
      [SHIM, ...args],
      { env: { ...process.env, AGENTMINUTES_BINARY: FAKE, FAKE_ECHO_ARGS: "1" } },
      (err, stdout) => {
        try {
          assert.equal(err, null);
          assert.deepEqual(JSON.parse(stdout), args);
          resolve();
        } catch (assertion) {
          reject(assertion);
        }
      },
    );
  });
});
