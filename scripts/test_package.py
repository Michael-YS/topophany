"""Regression tests use throwaway repositories, never local credentials."""
import importlib.util
import os
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

    def test_old_revision_evidence_is_rejected(self):
        from source_identity import source_identity, require_verified
        before = source_identity(self.root)
        summary = {"source_before": before, "source_after": before}
        old_files = {name: package.hashlib.sha256(body).hexdigest()
                     for name, body in package.files_to_export().items()}
        require_verified(summary, before["revision"], old_files)
        (self.root / "README.md").write_text("different revision\n")
        self.git("add", ".")
        self.commit()
        revision = self.git("rev-parse", "HEAD").decode().strip()
        files = {name: package.hashlib.sha256(body).hexdigest()
                 for name, body in package.files_to_export().items()}
        with self.assertRaisesRegex(RuntimeError, "revision"):
            require_verified(summary, revision, files)

    def test_mutation_during_verification_is_rejected(self):
        from source_identity import source_identity, require_unchanged, require_verified
        before = source_identity(self.root)
        (self.root / "README.md").write_text("changed while tests ran\n")
        after = source_identity(self.root)
        with self.assertRaisesRegex(RuntimeError, "changed during"):
            require_unchanged(before, after)
        with self.assertRaises(RuntimeError):
            require_verified({"source_before": before, "source_after": after},
                             before["revision"], before["files"])
        with self.assertRaisesRegex(RuntimeError, "manifest"):
            require_verified({"source_before": before, "source_after": before},
                             before["revision"], {"README.md": "wrong digest"})


    def test_ignored_source_is_part_of_verification_identity(self):
        from source_identity import source_identity, require_unchanged, require_verified
        before = source_identity(self.root)
        (self.root / ".git/info/exclude").write_text("internal/local_test.go\n")
        local = self.root / "internal/local_test.go"
        local.write_text("package local\n")
        after = source_identity(self.root)
        self.assertIn("internal/local_test.go", after["files"])
        self.assertFalse(after["clean"])
        with self.assertRaises(RuntimeError):
            require_unchanged(before, after)
        with self.assertRaises(RuntimeError):
            require_verified({"source_before": after, "source_after": after},
                             after["revision"], before["files"])

    @unittest.skipIf(os.name == "nt", "crafted POSIX filenames cannot be created on Windows")
    def test_nonportable_git_revision_is_rejected_before_export_and_identity(self):
        from source_identity import source_identity
        for name in ("topo\\main.go", "CON.go", "main.go."):
            with self.subTest(name=name):
                path = self.root / "internal" / name
                path.parent.mkdir(exist_ok=True)
                path.write_text("crafted source\n")
                self.git("add", "-f", "--", str(path))
                self.commit()
                with self.assertRaisesRegex(RuntimeError, "nonportable"):
                    package.files_to_export()
                with self.assertRaisesRegex(RuntimeError, "nonportable"):
                    source_identity(self.root)
                self.git("rm", "--", str(path))
                self.commit()


class PortablePathTests(unittest.TestCase):
    def test_rejects_platform_specific_names(self):
        from portable_paths import validate_paths
        for name in ("/cmd/main.go", "cmd//main.go", "cmd/./main.go", "cmd/../main.go",
                     "cmd/topo\\main.go", "cmd/..\\main.go", "C:/main.go", "//server/share",
                     "cmd/main.go:stream", "cmd/AUX", "cmd/con.txt", "cmd/CON .txt", "cmd/LPT¹.go",
                     "cmd/trailing.", "cmd/trailing ", "cmd/control\x00.go", "cmd/control\x7f.go",
                     "cmd/*.go", "cmd/surrogate\udcff.go"):
            with self.subTest(name=name), self.assertRaises(RuntimeError):
                validate_paths([name])

    def test_rejects_case_unicode_duplicate_and_directory_aliases(self):
        from portable_paths import validate_paths
        for names in (("cmd/main.go", "cmd/MAIN.go"),
                      ("docs/café.md", "docs/cafe\u0301.md"),
                      ("docs/I.md", "docs/ı.md"),
                      ("cmd/Topo/a.go", "cmd/topo/b.go"),
                      ("cmd/main.go", "cmd/main.go"),
                      ("cmd/topo", "cmd/topo/main.go"),
                      ("cmd/topo/main.go", "cmd/topo")):
            with self.subTest(names=names), self.assertRaises(RuntimeError):
                validate_paths(names)
        validate_paths(["cmd/topo/main.go", "cmd/topo/main_test.go", "docs/日本語.md"])

    def test_both_archive_manifests_require_exact_portable_members(self):
        expected = {"cmd/topo/main.go": "digest"}
        package.validate_archive_names(["topophany/cmd/topo/main.go"], expected)
        for names in (["topophany/cmd/topo\\main.go"],
                      ["topophany/cmd/topo/main.go", "topophany/cmd/topo/MAIN.go"],
                      ["topophany/cmd/topo/main.go"] * 2, [], ["elsewhere/main.go"]):
            with self.subTest(names=names), self.assertRaises(RuntimeError):
                package.validate_archive_names(names, expected)


if __name__ == "__main__":
    unittest.main()
