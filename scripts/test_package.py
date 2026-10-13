"""Regression tests use throwaway repositories, never local credentials."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("source_exporter", Path(__file__).with_name("package.py"))
package = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(package)


class SourceExportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.root_patch = patch.object(package, "ROOT", self.root)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.git("init", "-q")
        (self.root / "README.md").write_text("reviewed revision\n")
        (self.root / ".gitignore").write_text("*.pem\n*.key\n*.env\n.env\n.verify-build/\n")
        (self.root / "internal").mkdir()
        self.git("add", ".")
        self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, stderr=subprocess.STDOUT)

    def commit(self):
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")

    def test_ignored_secrets_and_untracked_source_fail_closed(self):
        for name in (".env", "local.env", "server.pem", "client.key", "extra.go"):
            path = self.root / "internal" / name
            path.write_text("synthetic secret")
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                package.files_to_export()
            path.unlink()

    def test_dirty_index_and_worktree_fail_closed(self):
        (self.root / "README.md").write_text("dirty")
        with self.assertRaises(RuntimeError):
            package.files_to_export()
        self.git("add", "README.md")
        with self.assertRaises(RuntimeError):
            package.files_to_export()

    def test_tracked_private_material_and_symlinks_fail_closed(self):
        path = self.root / "internal" / "server.key"
        path.write_text("synthetic secret")
        self.git("add", "-f", str(path))
        self.commit()
        with self.assertRaises(RuntimeError):
            package.files_to_export()
        self.git("rm", str(path))
        path = self.root / "internal" / "link"
        path.parent.mkdir(exist_ok=True)
        path.symlink_to("../README.md")
        self.git("add", str(path))
        self.commit()
        with self.assertRaises(RuntimeError):
            package.files_to_export()

    def test_archive_inputs_are_immutable_git_blobs(self):
        original_git = package.git

        def mutate_after_clean_check(*args):
            result = original_git(*args)
            if args[0] == "ls-tree":
                (self.root / "README.md").write_text("unreviewed change after validation")
            return result

        with patch.object(package, "git", side_effect=mutate_after_clean_check):
            files = package.files_to_export()
        self.assertEqual(files["README.md"], b"reviewed revision\n")
        self.assertEqual(set(files), {"README.md", ".gitignore"})


if __name__ == "__main__":
    unittest.main()
