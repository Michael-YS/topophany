"""Keep release trust boundaries reviewable without a YAML dependency."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class ReleaseBoundaryTests(unittest.TestCase):
    def test_all_executable_actions_are_immutable(self):
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            source = path.read_text()
            for action in re.findall(r"uses:\s*(\S+)", source):
                self.assertRegex(action, r"^[\w.-]+/[\w.-]+@[0-9a-f]{40}$")
            self.assertEqual(source.count("uses: actions/checkout@"), source.count("persist-credentials: false"))

    def test_write_authority_is_only_in_nonexecuting_publish_job(self):
        source = (ROOT / ".github/workflows/release.yml").read_text()
        build, publish = source.split("\n  publish:\n")
        self.assertNotIn("contents: write", build)
        self.assertIn("contents: read", build)
        self.assertIn("\n  test:\n", build)
        self.assertIn("\n  build:\n", build)
        self.assertEqual(build.count("ref: ${{ github.sha }}"), 2)
        self.assertIn("environment: release", publish)
        self.assertIn("contents: write", publish)
        self.assertIn("artifact-ids: ${{ needs.build.outputs.artifact-id }}", publish)
        self.assertIn("sha256sum --check --strict", publish)
        for forbidden in ("actions/checkout", "go test", "go build", "scripts/", "tar -", "unzip"):
            self.assertNotIn(forbidden, publish)


if __name__ == "__main__":
    unittest.main()
