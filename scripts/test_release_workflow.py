"""Exercise the actual publication argument selection without publishing."""
import os
from pathlib import Path
import subprocess
import unittest


class ReleaseFlags(unittest.TestCase):
    def test_actual_workflow_flags(self):
        workflow = (Path(__file__).resolve().parents[1] / ".github/workflows/release.yml").read_text()
        start = workflow.index("          release_flags=(--verify-tag)")
        end = workflow.index('          gh release create "$VERSION"', start)
        snippet = "\n".join(line[10:] for line in workflow[start:end].splitlines())
        # Print arguments exactly as gh would receive them, including the existing tag verification
        # for stable versions. No credentials or network command are available.
        script = 'set -euo pipefail\n' + snippet + '\nprintf "%s\\n" "${release_flags[@]}"'
        for tag, expected in [("v1.2.0", ["--verify-tag"]), ("v1.2.0-rc.1", ["--verify-tag", "--prerelease", "--latest=false"]),
                              ("v1.2.0-beta.2", ["--verify-tag", "--prerelease", "--latest=false"])]:
            with self.subTest(tag=tag):
                result = subprocess.run(["bash", "-c", script], env={**os.environ, "VERSION": tag},
                                        text=True, capture_output=True, check=True)
                self.assertEqual([x for x in result.stdout.splitlines() if x], expected)
        self.assertIn('--notes-file ./RELEASE_NOTES.md "${release_flags[@]}"', workflow)
        self.assertIn('[[ "$kind" == commit && "$sha" == "$SOURCE_SHA" ]]', workflow)


if __name__ == "__main__":
    unittest.main()
