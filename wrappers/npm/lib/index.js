"use strict";

const { binaryPath } = require("./binary");

// binaryPath locates the bundled CLI binary for callers who want to spawn
// it themselves; the pointer exports are kept for compatibility with the
// 0.0.1 name-reservation stub.
const PROJECT_URL = "https://github.com/agent-ecosystem/agentminutes";
const HOMEPAGE = "https://agentminutes.dev";

function projectUrl() {
  return PROJECT_URL;
}

module.exports = { binaryPath, PROJECT_URL, HOMEPAGE, projectUrl };
