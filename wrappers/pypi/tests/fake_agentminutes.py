#!/usr/bin/env python3
"""Fake agentminutes binary for wrapper tests; mirrors the npm wrapper's
fake: FAKE_* env vars drive the output, and FAKE_ECHO_ARGS prints received
argv so tests can assert passthrough."""

import json
import os
import sys

if os.environ.get("FAKE_ECHO_ARGS") == "1":
    json.dump(sys.argv[1:], sys.stdout)
    sys.exit(0)

sys.stdout.write(os.environ.get("FAKE_STDOUT", ""))
sys.stderr.write(os.environ.get("FAKE_STDERR", ""))
sys.exit(int(os.environ.get("FAKE_EXIT", "0")))
