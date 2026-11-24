"""The thinking level: reasoning_effort becomes claude -p --effort; an unknown one is 400. Stdlib only, nothing runs
claude:  python3 plugins/claude-code-bridge/test_bridge.py"""

import json
import os
import subprocess
import tempfile
import unittest
from unittest import mock

with tempfile.NamedTemporaryFile("w", suffix=".env", delete=False) as f:
    f.write("BRIDGE_TOKEN=test-token\n")
os.environ["BRIDGE_ENV"] = f.name
import bridge  # noqa: E402

os.unlink(f.name)

DONE = json.dumps({"subtype": "success", "result": "ok", "usage": {}})


def ran(body):
    with mock.patch.object(subprocess, "run", return_value=mock.Mock(stdout=DONE, stderr="", returncode=0)) as run:
        bridge.complete({"model": "claude-opus", "messages": [{"role": "user", "content": "hi"}]} | body)
    return run.call_args.args[0]


class Effort(unittest.TestCase):
    def test_each_level_is_passed(self):
        for level in ("low", "medium", "high"):
            cmd = ran({"reasoning_effort": level})
            self.assertEqual(cmd[cmd.index("--effort") + 1], level)

    def test_none_sent_means_the_clis_default(self):
        self.assertNotIn("--effort", ran({}))

    def test_an_unknown_level_is_400(self):
        with self.assertRaises(bridge.Fail) as e:
            ran({"reasoning_effort": "extreme"})
        self.assertEqual(e.exception.code, 400)


if __name__ == "__main__":
    unittest.main()
