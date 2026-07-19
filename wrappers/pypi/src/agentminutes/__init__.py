"""Python wrapper around the agentminutes Go CLI, which parses native
agent harness session logs (Antigravity CLI, Claude Code, Codex CLI) into
a unified event schema.

The wheel bundles the real binary and exposes it as the ``agentminutes``
console command. Set AGENTMINUTES_BINARY to override which binary runs.
"""

import os
import subprocess
import sys

PROJECT_URL = "https://github.com/agent-ecosystem/agentminutes"
HOMEPAGE = "https://agentminutes.dev"

__all__ = ["PROJECT_URL", "HOMEPAGE", "binary_path", "project_url"]


def project_url():
    """Return the URL of the agentminutes project repository."""
    return PROJECT_URL


def binary_path():
    """Return the path of the agentminutes binary this wrapper invokes."""
    override = os.environ.get("AGENTMINUTES_BINARY")
    if override:
        return override
    exe = "agentminutes.exe" if sys.platform == "win32" else "agentminutes"
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin", exe)
    if not os.path.exists(path):
        raise RuntimeError(
            "agentminutes: no bundled binary for this platform; install the Go "
            "CLI instead (https://github.com/agent-ecosystem/agentminutes) and "
            "set AGENTMINUTES_BINARY"
        )
    # Some installers drop the exec bit on package data; restore best-effort.
    if os.name == "posix" and not os.access(path, os.X_OK):
        try:
            os.chmod(path, 0o755)
        except OSError:
            pass
    return path


def _main():
    """Console-script entry: transparent passthrough to the binary."""
    binary = binary_path()
    argv = [binary] + sys.argv[1:]
    if os.name == "posix":
        os.execv(binary, argv)
    raise SystemExit(subprocess.call(argv))
