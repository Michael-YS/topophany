"""A successful go test exit alone cannot prove compatibility was exercised."""
import unittest
from check_compatibility import PACKAGE, TEST, require_pass


class CompatibilityGateTests(unittest.TestCase):
    def test_requires_the_named_test_to_finish_with_pass(self):
        for action in (None, "skip", "fail", "pass"):
            events = [{"Package": PACKAGE, "Action": "pass"}]
            if action:
                events.append({"Package": PACKAGE, "Test": TEST, "Action": action})
            self.assertEqual(require_pass(events), action == "pass")
        self.assertFalse(require_pass([{"Package": PACKAGE, "Test": TEST + "/child", "Action": "pass"}]))

    def test_test_jobs_fetch_history_and_require_the_fixture(self):
        from pathlib import Path
        import re
        root = Path(__file__).resolve().parents[1]
        for name, jobs in (("ci.yml", ("test", "acceptance")), ("release.yml", ("test",)), ("coverage.yml", ("coverage",))):
            source = (root / ".github/workflows" / name).read_text()
            for job in jobs:
                part = re.split(r"\n  [a-z][\w-]*:\n", source.split("\n  " + job + ":\n")[1])[0]
                self.assertIn("fetch-depth: 0", part)
                self.assertIn('TOPO_REQUIRE_OLD_READER: "1"', part)
