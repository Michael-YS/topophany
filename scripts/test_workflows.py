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
        self.assertIn('git show "$SOURCE_SHA:CHANGELOG.md" > dist/CHANGELOG.md', build)
        self.assertIn('git show "$SOURCE_SHA:docs/releases/$VERSION.md" > dist/RELEASE_NOTES.md', build)
        self.assertIn("--notes-file ./RELEASE_NOTES.md", publish)
        self.assertIn("./CHANGELOG.md ./RELEASE_NOTES.md", publish)
        for forbidden in ("actions/checkout", "go test", "go build", "scripts/", "tar -", "unzip"):
            self.assertNotIn(forbidden, publish)

    def test_release_binaries_carry_the_tag_version(self):
        # The build checks out one commit without tags, where the toolchain
        # can stamp only a pseudo-version; the binary must say the release.
        build = (ROOT / ".github/workflows/release.yml").read_text().split("\n  publish:\n")[0]
        builds = [line for line in build.splitlines() if "go build" in line]
        self.assertEqual(len(builds), 1)
        self.assertIn("-X github.com/WhitenWhiten/topophany/internal/model.Version=${VERSION#v}", builds[0])
        self.assertIn('[ "$("$stage/$bin" version)" = "Topophany ${VERSION#v}" ]', build)

class ReleaseTagBindingTests(unittest.TestCase):
    def test_publish_resolves_lightweight_and_nested_annotated_tags(self):
        import os
        import subprocess
        import tempfile
        source = (ROOT / ".github/workflows/release.yml").read_text().split("\n  publish:\n")[1]
        self.assertIn("SOURCE_SHA: ${{ github.sha }}", source)
        script = source.split("        run: |\n")[1]
        script = "\n".join(line[10:] for line in script.splitlines())
        for mode in ("matching", "moved", "annotated", "nested", "cycle", "missing"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / "dist").mkdir()
                for name in ("fixture.tar.gz", "fixture.zip"):
                    (root / "dist" / name).write_bytes(b"inert archive")
                (root / "dist/CHANGELOG.md").write_text("fixture changelog\n")
                (root / "dist/RELEASE_NOTES.md").write_text("fixture notes\n\nSource commit: expected\n")
                checksums = subprocess.check_output(["sha256sum", "fixture.tar.gz", "fixture.zip", "CHANGELOG.md", "RELEASE_NOTES.md"], cwd=root / "dist")
                (root / "dist/SHA256SUMS.txt").write_bytes(checksums)
                gh = root / "gh"
                gh.write_text('''#!/bin/bash
set -eu
if [ "$1" = release ]; then
  printf '%s\\n' "$@" > "$PUBLISHED"
  cat RELEASE_NOTES.md > "$PUBLISHED_NOTES"
  exit 0
fi
case "$MODE:$2" in
 missing:*) exit 1;;
 moved:*) printf 'commit\\tother\\n';;
 cycle:*) printf 'tag\\tcycle\\n';;
 annotated:*/ref/*|nested:*/ref/*) printf 'tag\\touter\\n';;
 nested:*/tags/outer) printf 'tag\\tinner\\n';;
 *) printf 'commit\\texpected\\n';;
esac
''')
                gh.chmod(0o755)
                env = dict(os.environ, PATH=tmp + os.pathsep + os.environ["PATH"], MODE=mode,
                           SOURCE_SHA="expected", VERSION="v1.2.3", GH_REPO="test/repo",
                           PUBLISHED=str(root / "published"), PUBLISHED_NOTES=str(root / "published-notes"))
                result = subprocess.run(["bash", "-c", script], cwd=root, env=env, capture_output=True)
                success = mode in {"matching", "annotated", "nested"}
                self.assertEqual(result.returncode == 0, success, result.stderr.decode())
                self.assertEqual((root / "published").exists(), success)
                if success:
                    self.assertIn("--notes-file", (root / "published").read_text())
                    self.assertIn("Source commit: expected", (root / "published-notes").read_text())


if __name__ == "__main__":
    unittest.main()
