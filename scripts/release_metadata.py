#!/usr/bin/env python3
"""Stage the release's documentation from regular files in an immutable Git tree."""
import argparse
from fnmatch import fnmatchcase
from pathlib import Path
import subprocess

METADATA = ("LICENSE", "README.md", "README.zh.md", "README.ja.md", "CHANGELOG.md")


def stage_metadata(root: Path, revision: str, destination: Path) -> None:
    def git(*args: str) -> bytes:
        return subprocess.check_output(["git", *args], cwd=root)

    revision = git("rev-parse", "--verify", revision + "^{commit}").decode().strip()
    entries = {}
    for entry in git("ls-tree", "-z", revision).split(b"\0"):
        if not entry:
            continue
        meta, name = entry.split(b"\t", 1)
        name = name.decode("utf-8")
        if name not in METADATA and not fnmatchcase(name, "README*.md"):
            continue
        mode, kind, oid = meta.split()
        if name not in METADATA or mode != b"100644" or kind != b"blob":
            raise RuntimeError("unexpected or nonregular release metadata: " + name)
        entries[name] = oid.decode()
    if set(entries) != set(METADATA):
        raise RuntimeError("release metadata is missing from the Git revision")
    # Validate the whole manifest before reading any content. Git blob reads
    # cannot follow a worktree symlink or race a change to its target.
    for name in METADATA:
        body = git("cat-file", "blob", entries[name])
        with (destination / name).open("xb") as output:
            output.write(body)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    stage_metadata(Path(__file__).resolve().parents[1], args.revision, args.destination)


if __name__ == "__main__":
    main()
