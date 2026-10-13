#!/usr/bin/env python3
"""Export source ZIP/tar.gz, read every ZIP member, rebuild the actual ZIP copy.

Requires a successful scripts/verify.py run. Never packages files outside this
source root, binaries, runtime demo directories, or environment credentials.
Outputs truthful machine-readable checks and hashes; fails closed on any error.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
ALLOWED_ROOT_FILES = {"go.mod", "go.sum", "README.md", "LICENSE", "SECURITY.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md",
                      "CHANGELOG.md", "Makefile", ".gitignore", ".gitattributes", ".editorconfig"}
ALLOWED_ROOT_DIRS = {"cmd", "internal", "scripts", "docs", "deploy", "examples", ".github"}
EXCLUDED_PARTS = {"__pycache__", ".git", ".verify-build", "bin", "dist", "demo"}


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def files_to_export() -> list[Path]:
    result = []
    for path in sorted(ROOT.rglob("*")):
        rel = path.relative_to(ROOT)
        if any(part in EXCLUDED_PARTS for part in rel.parts):
            continue
        if path.is_symlink():
            raise RuntimeError("source tree contains a symlink: " + str(rel))
        if not path.is_file():
            continue
        if len(rel.parts) == 1 and rel.name not in ALLOWED_ROOT_FILES:
            raise RuntimeError("unreviewed file at source root: " + str(rel))
        if len(rel.parts) > 1 and rel.parts[0] not in ALLOWED_ROOT_DIRS:
            raise RuntimeError("unreviewed directory at source root: " + str(rel))
        if path.suffix in {".exe", ".pyc", ".zip", ".gz"} or rel.name in {"topo", "auth.json", "credentials.json", "worker-credentials.json", "NETWORK-MEMO.md"}:
            raise RuntimeError("runtime/private artifact in source tree: " + str(rel))
        result.append(path)
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--name", default="Topophany-0.1.0-source")
    parser.add_argument("--verification", type=Path, default=ROOT / ".verify-build" / "verification",
                        help="output directory of a passing scripts/verify.py run")
    args = parser.parse_args()
    if Path(args.name).name != args.name or not args.name.startswith("Topophany-"):
        parser.error("name must be a Topophany- prefixed basename")
    output = args.output.resolve()
    if output == ROOT or ROOT in output.parents:
        parser.error("output must be outside the source tree")
    output.mkdir(parents=True, exist_ok=True)
    summary = json.loads((args.verification / "summary.json").read_text())
    if summary.get("status") != "pass" or summary.get("quality_gate_failures"):
        raise RuntimeError("refusing release: verification is missing or failing")
    source_files = files_to_export()
    source_sha = {str(p.relative_to(ROOT)): digest(p) for p in source_files}
    zip_path = output / (args.name + ".zip")
    tar_path = output / (args.name + ".tar.gz")
    with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
        for path in source_files:
            zf.write(path, "topophany/" + str(path.relative_to(ROOT)))
    with tarfile.open(tar_path, "w:gz", compresslevel=9) as tf:
        for path in source_files:
            tf.add(path, arcname="topophany/" + str(path.relative_to(ROOT)), recursive=False)
    env = os.environ.copy()
    env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="4")
    rebuild_log = output / "Topophany-archive-rebuild.log"
    commands = []
    with tempfile.TemporaryDirectory(prefix="topophany-zip-validation-") as tmp:
        dest = Path(tmp)
        with zipfile.ZipFile(zip_path, "r") as zf:
            bad_member = zf.testzip()
            if bad_member:
                raise RuntimeError("ZIP CRC/read failure: " + bad_member)
            for info in zf.infolist():
                p = Path(info.filename)
                if p.is_absolute() or ".." in p.parts or not p.parts or p.parts[0] != "topophany":
                    raise RuntimeError("unsafe ZIP member: " + info.filename)
            zf.extractall(dest)
        extracted = dest / "topophany"
        for rel, expected in source_sha.items():
            if digest(extracted / rel) != expected:
                raise RuntimeError("extracted file differs: " + rel)
        with tarfile.open(tar_path, "r:gz") as tf:
            names = tf.getnames()
            if sorted(names) != sorted("topophany/" + name for name in source_sha):
                raise RuntimeError("tar member manifest differs")
            for member in tf:
                body = tf.extractfile(member)
                if not member.isfile() or body is None:
                    raise RuntimeError("nonregular tar member")
                with body:
                    if hashlib.sha256(body.read()).hexdigest() != source_sha[member.name[len("topophany/"):]]:
                        raise RuntimeError("tar content differs")
        with rebuild_log.open("w", encoding="utf-8") as log:
            for command in (["go", "test", "-count=1", "./..."],
                            ["go", "build", "-trimpath", "-o", str(dest / "topo"), "./cmd/topo"]):
                log.write("$ " + " ".join(command) + "\n")
                log.flush()
                result = subprocess.run(command, cwd=extracted, env=env, stdout=log, stderr=subprocess.STDOUT, timeout=180)
                commands.append({"command": command, "exit_code": result.returncode})
                if result.returncode:
                    raise RuntimeError("actual ZIP source rebuild failed; see " + str(rebuild_log))
            log.write("ZIP CRC, all source SHA-256 matches, tar member/content validation and extracted-source tests/build: PASS\n")
    report_path = output / "Topophany-test-report.md"
    shutil.copyfile(args.verification / "FINAL-TEST-REPORT.md", report_path)
    with report_path.open("a", encoding="utf-8") as report:
        report.write("\n## Archive Rebuild Verification\n\nThe exported ZIP was verified per-member with CRC checks; all extracted file SHA-256 digests matched the source tree. The tar.gz archive content matched per-file as well. **Rebuilding from the extracted ZIP directory via `go test -count=1 ./...` and `go build` both returned 0.** For details, see Topophany-archive-rebuild.log and the delivery manifest included in this release.\n")
    manifest = {
        "schema": 1, "project": "Topophany", "version": "0.1.0",
        "exported_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
        "original_source_recovered": False,
        "verification": summary,
        "source_files": len(source_files), "go_files": summary["go_files"], "go_test_files": summary["go_test_files"],
        "archive_member_integrity": "pass", "extracted_source_matches": True,
        "archive_rebuild_exit_code": 0, "archive_rebuild_commands": commands,
        "archives": [{"file": p.name, "bytes": p.stat().st_size, "sha256": digest(p)} for p in (zip_path, tar_path)],
        "source_sha256": source_sha,
        "excluded": ["user network memo", "user image", "production credentials", "runtime data", "compiled executables"],
    }
    manifest_path = output / "Topophany-delivery-manifest.json"
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    hash_paths = [zip_path, tar_path, report_path, rebuild_log, manifest_path]
    (output / "Topophany-SHA256SUMS.txt").write_text("".join(digest(p) + "  " + p.name + "\n" for p in hash_paths), encoding="utf-8")
    print(json.dumps({"status": "pass", "archives": manifest["archives"], "source_files": len(source_files), "archive_rebuild_exit_code": 0}, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
