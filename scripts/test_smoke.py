import contextlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import smoke


class SmokePlatformIsolation(unittest.TestCase):
    def test_non_linux_never_starts_processes_or_network(self):
        for platform in ("Darwin", "Windows"):
            with self.subTest(platform=platform), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "smoke.json"
                with patch.object(smoke.platform, "system", return_value=platform), \
                     patch.object(smoke.subprocess, "Popen", side_effect=AssertionError("process started")), \
                     patch.object(smoke.subprocess, "run", side_effect=AssertionError("login invoked")), \
                     patch.object(smoke.http.server, "ThreadingHTTPServer", side_effect=AssertionError("network started")), \
                     patch("sys.argv", ["smoke.py", "--binary", "/must-not-run", "--output", str(output)]), \
                     contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(smoke.main(), 0)
                report = json.loads(output.read_text())
                self.assertEqual(report["status"], "skipped")
                self.assertEqual(report["checks"], [])
