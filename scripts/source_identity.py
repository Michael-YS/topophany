"""Bind verification evidence to a commit and the exact source bytes tested."""
import hashlib
import os
from pathlib import Path
import re
import subprocess

from portable_paths import validate_paths


ALLOWED_ROOT_FILES = {"go.mod", "go.sum", "README.md", "LICENSE", "SECURITY.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md",
                      "CHANGELOG.md", "Makefile", ".gitignore", ".gitattributes", ".editorconfig"}
ALLOWED_ROOT_DIRS = {"cmd", "internal", "scripts", "docs", "deploy", "examples", ".github"}
EXCLUDED_PARTS = {"__pycache__", ".git", ".verify-build", "bin", "dist", "demo"}


# The README in another language: README.<language>.md.
TRANSLATED_README = re.compile(r"README\.[a-z]{2,3}(-[A-Za-z0-9]+)*\.md")


def accepted(rel: Path) -> bool:
    return not any(part in EXCLUDED_PARTS for part in rel.parts) and (
        len(rel.parts) == 1 and (rel.name in ALLOWED_ROOT_FILES or TRANSLATED_README.fullmatch(rel.name)) or
        len(rel.parts) > 1 and rel.parts[0] in ALLOWED_ROOT_DIRS)


def source_identity(root: Path) -> dict:
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=root)

    revision = git("rev-parse", "--verify", "HEAD^{commit}").decode().strip()
    # Include ignored source too: Go compiles ignored .go files. Build outputs
    # and Python caches remain excluded by the same policy as source export.
    names = set(git("ls-files", "-z", "--cached", "--others", "--exclude-standard").split(b"\0"))
    ignored_sources = {name for name in git("ls-files", "-z", "--others", "--ignored", "--exclude-standard").split(b"\0")
                       if name and accepted(Path(os.fsdecode(name)))}
    names.update(ignored_sources)
    validate_paths(os.fsdecode(name) for name in names - {b""})
    files = {}
    for name in sorted(names - {b""}):
        path = root / os.fsdecode(name)
        if path.is_symlink() or not path.is_file():
            raise RuntimeError("nonregular or missing source input: " + os.fsdecode(name))
        files[os.fsdecode(name)] = hashlib.sha256(path.read_bytes()).hexdigest()
    return {"revision": revision, "files": files,
            "clean": not ignored_sources and not bool(git("status", "--porcelain", "--untracked-files=all").strip())}


def require_unchanged(before: dict, after: dict) -> None:
    if before != after:
        raise RuntimeError("source identity changed during verification")


def require_verified(summary: dict, revision: str, files: dict[str, str]) -> None:
    before, after = summary.get("source_before"), summary.get("source_after")
    if not before or before != after or not before.get("clean") or before.get("revision") != revision:
        raise RuntimeError("verification does not match a clean immutable source revision")
    if any(before.get("files", {}).get(name) != digest for name, digest in files.items()):
        raise RuntimeError("verification source manifest differs from exported blobs")
