"""Wrapper tests against the fake binary; run from wrappers/pypi with
``python3 -m unittest discover -s tests``."""

import json
import os
import subprocess
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "src"))
import agentminutes  # noqa: E402

FAKE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fake_agentminutes.py")


class WrapperTest(unittest.TestCase):
    def setUp(self):
        self._saved = os.environ.copy()

    def tearDown(self):
        os.environ.clear()
        os.environ.update(self._saved)

    def test_binary_path_honors_override(self):
        os.environ["AGENTMINUTES_BINARY"] = FAKE
        self.assertEqual(agentminutes.binary_path(), FAKE)

    def test_binary_path_explains_missing_bundled_binary(self):
        # In the source tree no binary is bundled, so the error must carry
        # the install-the-Go-CLI/override guidance.
        os.environ.pop("AGENTMINUTES_BINARY", None)
        with self.assertRaisesRegex(RuntimeError, "AGENTMINUTES_BINARY"):
            agentminutes.binary_path()

    def test_project_url_still_points_at_the_repository(self):
        self.assertEqual(
            agentminutes.project_url(),
            "https://github.com/agent-ecosystem/agentminutes",
        )

    def test_main_passes_argv_through(self):
        args = ["convert", "--format", "jsonl", "--", "weird name.jsonl"]
        proc = subprocess.run(
            [
                sys.executable,
                "-c",
                "import sys; sys.path.insert(0, %r); "
                "import agentminutes; "
                "sys.argv = ['agentminutes'] + %r; agentminutes._main()"
                % (os.path.join(os.path.dirname(FAKE), "..", "src"), args),
            ],
            env={**os.environ, "AGENTMINUTES_BINARY": FAKE, "FAKE_ECHO_ARGS": "1"},
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout), args)

    def test_main_propagates_exit_code(self):
        proc = subprocess.run(
            [
                sys.executable,
                "-c",
                "import sys; sys.path.insert(0, %r); "
                "import agentminutes; sys.argv = ['agentminutes']; agentminutes._main()"
                % os.path.join(os.path.dirname(FAKE), "..", "src"),
            ],
            env={**os.environ, "AGENTMINUTES_BINARY": FAKE, "FAKE_EXIT": "3"},
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(proc.returncode, 3)


if __name__ == "__main__":
    unittest.main()
