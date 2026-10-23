"""Keep workflow triggers and release boundaries reviewable without YAML dependencies."""
from pathlib import Path
import fnmatch
import re
import unittest

from test_release_workflow import archive_fixtures

ROOT = Path(__file__).resolve().parents[1]


def event_paths(workflow, event):
    header = (ROOT / ".github/workflows" / workflow).read_text().split("\njobs:\n")[0]
    section = re.search(rf"^  {event}:\n(.*?)(?=^  \w+:|^\S|\Z)", header,
                        re.MULTILINE | re.DOTALL)
    if section is None:
        raise AssertionError(f"Missing {event} trigger in {workflow}")
    return re.findall(r"^      - '([^']+)'$", section[1], re.MULTILINE)


def triggers_for(patterns, files):
    # These workflow patterns use directory/**, exact paths and **/filename
    # exclusions. A leading **/ also matches a file in the repository root.
    for path in files:
        included = False
        for pattern in patterns:
            exclude = pattern.startswith("!")
            glob = pattern.removeprefix("!")
            if fnmatch.fnmatchcase(path, glob) or (
                    glob.startswith("**/") and fnmatch.fnmatchcase(path, glob[3:])):
                included = not exclude
        if included:
            return True
    return False


class WorkflowPathFilterTests(unittest.TestCase):
    def test_source_tests_and_build_configuration_trigger_ci(self):
        filters = [event_paths("ci.yml", "push"), event_paths("ci.yml", "pull_request"),
                   event_paths("coverage.yml", "pull_request")]
        self.assertEqual(filters[0], filters[1])
        self.assertEqual(filters[0], filters[2])
        for patterns in filters:
            self.assertTrue(patterns, "Automatic test triggers need explicit path filters")
            for path in ("cmd/topo/main.go", "internal/direct/config.go",
                         "internal/direct/http_test.go", "internal/preview/locales/zh.json",
                         "internal/adapters/beszel/testdata/system_stats.json",
                         "scripts/test_workflows.py", "scripts/verify.py",
                         "examples/beszel.json", "deploy/topophany-worker.service",
                         "docs/contracts/direct-v1/fixtures/capabilities.json",
                         "docs/contracts/direct-v1/manifest.json", "go.mod", "go.sum",
                         "Makefile", ".github/workflows/ci.yml",
                         ".github/workflows/coverage.yml", ".github/workflows/release.yml"):
                with self.subTest(path=path, patterns=patterns):
                    self.assertTrue(triggers_for(patterns, [path]))

    def test_documentation_and_site_changes_do_not_trigger_test_workflows(self):
        for workflow, event in (("ci.yml", "push"), ("ci.yml", "pull_request"),
                                ("coverage.yml", "pull_request")):
            patterns = event_paths(workflow, event)
            for files in (["README.md", "README.zh.md", "CONTRIBUTING.md", "CHANGELOG.md"],
                          ["docs/DIRECT.md", "docs/assets/banner.png"],
                          ["internal/adapters/beszel/README.md"],
                          ["docs/contracts/direct-v1/contract.md"],
                          ["scripts/README", "examples/runcat/README.md"],
                          ["internal/guide.rst", "scripts/guide.adoc", "examples/guide.mdx"],
                          ["site/src/index.html", "site/src/locales/zh.json"],
                          ["site/package.json", "site/wrangler.jsonc", "site/tests/browser.mjs"],
                          ["site/go.mod", "site/public/data/tour.zh.json"]):
                with self.subTest(workflow=workflow, event=event, files=files):
                    self.assertFalse(triggers_for(patterns, files))
            self.assertTrue(triggers_for(patterns, ["README.md", "internal/direct/http_test.go"]))
            self.assertTrue(triggers_for(patterns, ["site/src/index.html", "cmd/topo/main.go"]))


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
        self.assertIn('git show "$SOURCE_SHA:CHANGELOG.md" > "$dist/CHANGELOG.md"', build)
        self.assertIn('git show "$SOURCE_SHA:docs/releases/$VERSION.md" > "$dist/RELEASE_NOTES.md"', build)
        self.assertIn("--notes-file ./RELEASE_NOTES.md", publish)
        self.assertIn("./CHANGELOG.md ./RELEASE_NOTES.md", publish)
        for forbidden in ("actions/checkout", "go test", "go build", "scripts/", "tar -", "unzip"):
            self.assertNotIn(forbidden, publish)

    def test_release_requires_vulnerability_check(self):
        source = (ROOT / ".github/workflows/release.yml").read_text()
        test, build = source.split("\n  build:\n")
        self.assertIn("go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...", test)
        self.assertIn("    needs: test", build)

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
                archive_fixtures(root / "dist")
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
