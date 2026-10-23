"""Exercise site check selection against real push and pull request histories."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from site_changes import site_changed


class SiteChangeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-b", "main")
        self.git("config", "user.name", "CI test")
        self.git("config", "user.email", "ci@example.invalid")
        self.write("internal/main.go", "package main\n")
        self.write("site/src/index.html", "<p>site</p>\n")
        self.base = self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.PIPE).decode().strip()

    def write(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-m", "test change")
        return self.git("rev-parse", "HEAD")

    def push(self, base=None):
        return {"before": base or self.base, "after": self.git("rev-parse", "HEAD")}

    def test_source_only_push_skips_site(self):
        self.write("internal/main.go", "package main\n// source changed\n")
        self.commit()
        self.assertFalse(site_changed("push", self.push(), self.root))

    def test_site_and_mixed_pushes_check_site(self):
        self.write("site/src/index.html", "<p>new site</p>\n")
        site_revision = self.commit()
        self.assertTrue(site_changed("push", self.push(), self.root))
        self.write("internal/main.go", "package main\n// source changed\n")
        self.commit()
        self.assertTrue(site_changed("push", self.push(), self.root))
        self.assertFalse(site_changed("push", self.push(site_revision), self.root))

    def test_documentation_inside_site_does_not_select_a_build(self):
        for name in ("README", "README.txt", "nested/README.zh.md", "guide.md", "guide.mdx",
                     "nested/guide.rst", "nested/guide.adoc"):
            self.write("site/" + name, "documentation\n")
        self.commit()
        self.assertFalse(site_changed("push", self.push(), self.root))

    def test_site_deletions_and_moves_out_select_checks(self):
        self.git("mv", "site/src/index.html", "index.html")
        self.commit()
        self.assertTrue(site_changed("push", self.push(), self.root))

    def test_new_branch_compares_against_an_empty_tree(self):
        self.assertTrue(site_changed("push", self.push("0" * 40), self.root))
        (self.root / "site/src/index.html").unlink()
        self.commit()
        self.assertFalse(site_changed("push", self.push("0" * 40), self.root))

    def test_pull_request_ignores_unrelated_base_branch_site_changes(self):
        self.git("checkout", "-b", "feature")
        self.write("internal/main.go", "package main\n// PR source change\n")
        head = self.commit()
        self.git("checkout", "main")
        self.write("site/src/index.html", "<p>unrelated base change</p>\n")
        base = self.commit()
        event = {"pull_request": {"base": {"sha": base}, "head": {"sha": head}}}
        self.assertFalse(site_changed("pull_request", event, self.root))
        self.git("checkout", "feature")
        self.write("site/src/index.html", "<p>PR site change</p>\n")
        event["pull_request"]["head"]["sha"] = self.commit()
        self.assertTrue(site_changed("pull_request", event, self.root))

    def test_manual_runs_require_an_explicit_site_opt_in(self):
        for value, expected in ((True, True), ("true", True), (False, False),
                                ("false", False), (None, False)):
            with self.subTest(value=value):
                event = {"inputs": {"site": value}}
                self.assertEqual(site_changed("workflow_dispatch", event, self.root), expected)
        self.assertFalse(site_changed("workflow_dispatch", {}, self.root))

    def test_missing_revision_fails_instead_of_silently_skipping_checks(self):
        event = {"before": "1" * 40, "after": self.base}
        with self.assertRaises(subprocess.CalledProcessError):
            site_changed("push", event, self.root)

    def test_event_file_writes_a_step_output(self):
        event_file, output = self.root / "event.json", self.root / "output"
        event_file.write_text(json.dumps(self.push()))
        output.write_text("existing=value\n")
        env = dict(os.environ, GITHUB_EVENT_NAME="push", GITHUB_EVENT_PATH=str(event_file),
                   GITHUB_OUTPUT=str(output))
        subprocess.run([sys.executable, str(Path(__file__).with_name("site_changes.py"))],
                       cwd=self.root, env=env, check=True, capture_output=True)
        self.assertEqual(output.read_text(), "existing=value\nchanged=false\n")


if __name__ == "__main__":
    unittest.main()
