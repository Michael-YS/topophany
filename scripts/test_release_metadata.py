"""Release staging must never dereference repository or runner symlinks."""
from pathlib import Path
import subprocess
import tempfile
import unittest

from release_metadata import METADATA, stage_metadata


class ReleaseMetadataTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "repo"
        self.root.mkdir()
        self.stage = Path(self.temp.name) / "stage"
        self.stage.mkdir()
        self.git("init", "-q")
        for name in METADATA:
            (self.root / name).write_text("reviewed " + name + "\n")
        self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.STDOUT)

    def commit(self):
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                 "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")

    def test_release_metadata_reads_only_frozen_regular_blobs(self):
        revision = self.git("rev-parse", "HEAD").decode().strip()
        outside = Path(self.temp.name) / "runner.txt"
        outside.write_text("synthetic runner data")
        (self.root / "README.md").unlink()
        (self.root / "README.md").symlink_to(outside)
        stage_metadata(self.root, revision, self.stage)
        self.assertEqual({p.name for p in self.stage.iterdir()}, set(METADATA))
        for name in METADATA:
            self.assertFalse((self.stage / name).is_symlink())
            self.assertEqual((self.stage / name).read_text(), "reviewed " + name + "\n")

    def test_release_metadata_rejects_symlinks_before_staging(self):
        for name in (*METADATA, "README.runner.md"):
            with self.subTest(name=name):
                self.git("reset", "--hard", "HEAD")
                path = self.root / name
                if path.exists():
                    path.unlink()
                path.symlink_to("/synthetic-runner-file-that-must-never-be-read")
                self.git("add", name)
                tree = self.git("write-tree").decode().strip()
                # commit-tree makes a candidate tagged revision without moving HEAD.
                revision = self.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid",
                                    "commit-tree", tree, "-m", "symlink fixture").decode().strip()
                with self.assertRaisesRegex(RuntimeError, "nonregular"):
                    stage_metadata(self.root, revision, self.stage)
                self.assertEqual(list(self.stage.iterdir()), [])

    def test_release_metadata_rejects_unexpected_or_missing_inputs(self):
        (self.root / "README.extra.md").write_text("unexpected")
        self.commit()
        with self.assertRaisesRegex(RuntimeError, "unexpected"):
            stage_metadata(self.root, "HEAD", self.stage)
        self.git("rm", "README.extra.md", "LICENSE")
        self.commit()
        with self.assertRaisesRegex(RuntimeError, "missing"):
            stage_metadata(self.root, "HEAD", self.stage)
        self.assertEqual(list(self.stage.iterdir()), [])

    def test_workflow_uses_immutable_metadata_staging(self):
        source = (Path(__file__).resolve().parents[1] / ".github/workflows/release.yml").read_text()
        build = source.split("\n  publish:\n")[0]
        self.assertIn("SOURCE_SHA: ${{ github.sha }}", build)
        self.assertIn('python3 scripts/release_metadata.py --revision "$SOURCE_SHA" --destination "$stage"', build)
        self.assertNotIn("cp LICENSE", build)


if __name__ == "__main__":
    unittest.main()
