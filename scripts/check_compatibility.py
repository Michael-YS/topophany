#!/usr/bin/env python3
"""Require the named real old-reader test to pass, including fixture setup."""
import json
import os
from pathlib import Path
import subprocess
import sys

TEST = "TestOldBinaryRefusesNewExternalData"
PACKAGE = "github.com/WhitenWhiten/topophany/internal/store"


def require_pass(events):
    outcomes = [e["Action"] for e in events
                if e.get("Package") == PACKAGE and e.get("Test") == TEST
                and e.get("Action") in {"pass", "fail", "skip"}]
    return outcomes == ["pass"]


def main():
    result = subprocess.run(
        ["go", "test", "-count=1", "-json", "-run", "^" + TEST + "$", "./internal/store"],
        cwd=Path(__file__).resolve().parents[1], capture_output=True, text=True,
        env={**os.environ, "TOPO_REQUIRE_OLD_READER": "1"})
    sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)
    events = [json.loads(line) for line in result.stdout.splitlines() if line.startswith("{")]
    if result.returncode or not require_pass(events):
        print("Real old-reader compatibility test did not pass.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
