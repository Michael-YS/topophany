#!/usr/bin/env python3
"""Generate internal/view/colos.go from Cloudflare's public locations list.

Source: https://speed.cloudflare.com/locations — a JSON array of objects with
``iata``, ``lat``, ``lon``, ``cca2``, ``region`` and ``city`` (309 entries on
2026-10-19). The endpoint answers only to a browser-like client; curl's
default user agent receives ``{}`` instead of the array, so the fetch sends a
browser's user agent, the site's referer and a JSON accept header:

    curl -sS -H 'Referer: https://speed.cloudflare.com/' \\
      -H 'Accept: application/json, text/plain, */*' \\
      -A 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140 Safari/537.36' \\
      https://speed.cloudflare.com/locations

Usage:

    python3 scripts/gen_colos.py
        Fetch the live list and write internal/view/colos.go.

    python3 scripts/gen_colos.py --input internal/view/testdata/cloudflare-locations-2026-10-19.json
        Regenerate from a saved capture, offline and byte for byte.

    python3 scripts/gen_colos.py --check [--input FILE]
        Write nothing; exit 1 when the checked-in file is not what the input
        generates.

The generated header records the source URL, the capture date, the entry
count and the SHA-256 of the exact input bytes, as the land-mask generator
names its source. With --input the date comes from the capture file's
YYYY-MM-DD name unless --date overrides it, so regenerating from the
checked-in snapshot reproduces the checked-in file. The generated table is
plain data; no request is made at runtime.

Validation follows Cloudflare's documented fields: iata is exactly three
uppercase ASCII letters, lat and lon are finite and within the world, cca2 is
two uppercase letters, and no code is repeated or dropped. The live list has
three Indian locations (AIP, AGR, LUH) with an empty city, so a city is 0..64
printable characters rather than the 1..64 one might expect; the table keeps
them without a city and the graph names them by code.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
from pathlib import Path
import re
import sys
import urllib.request
from typing import NoReturn

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "internal" / "view" / "colos.go"
SOURCE = "https://speed.cloudflare.com/locations"
USER_AGENT = ("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
              "AppleWebKit/537.36 Chrome/140 Safari/537.36")
MAX_ENTRIES = 4096
MAX_BYTES = 2 << 20
CODE = re.compile(r"^[A-Z]{3}$")
CCA2 = re.compile(r"^[A-Z]{2}$")


def fail(message: str) -> NoReturn:
    raise SystemExit("gen_colos: " + message)


def fetch(url: str) -> bytes:
    """Fetch the locations array as the browser-shaped request Cloudflare wants."""
    request = urllib.request.Request(url, headers={
        "Referer": "https://speed.cloudflare.com/",
        "Accept": "application/json, text/plain, */*",
        "User-Agent": USER_AGENT,
    })
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            if response.status != 200:
                fail(f"{url} returned HTTP {response.status}")
            raw = response.read(MAX_BYTES + 1)
    except OSError as error:
        fail(f"fetching {url} failed: {error}")
    if len(raw) > MAX_BYTES:
        fail(f"{url} returned more than {MAX_BYTES} bytes")
    return raw


def parse(raw: bytes) -> list[dict]:
    """Validate the locations array and return it sorted by IATA code."""
    try:
        data = json.loads(raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        fail(f"input is not JSON: {error}")
    if not isinstance(data, list):
        fail("input must be a JSON array, not " + type(data).__name__)
    if not 1 <= len(data) <= MAX_ENTRIES:
        fail(f"input holds {len(data)} entries; want 1..{MAX_ENTRIES}")
    rows, seen = [], set()
    for index, item in enumerate(data):
        if not isinstance(item, dict):
            fail(f"entry {index} is {type(item).__name__}, not an object")
        code = item.get("iata")
        if not isinstance(code, str) or not CODE.fullmatch(code):
            fail(f"entry {index}: iata must be exactly three uppercase ASCII letters")
        if code in seen:
            fail(f"duplicate iata {code}")
        seen.add(code)
        point = {}
        for name, low, high in (("lat", -90.0, 90.0), ("lon", -180.0, 180.0)):
            value = item.get(name)
            if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
                fail(f"{code}: {name} must be a finite number")
            if not low <= value <= high:
                fail(f"{code}: {name} {value} is outside {low:g}..{high:g}")
            point[name] = float(value)
        city = item.get("city")
        if not isinstance(city, str):
            fail(f"{code}: city must be a string")
        if len(city) > 64:
            fail(f"{code}: city is longer than 64 characters")
        if any(not character.isprintable() for character in city):
            fail(f"{code}: city contains a non-printable character")
        cca2 = item.get("cca2")
        if not isinstance(cca2, str) or not CCA2.fullmatch(cca2):
            fail(f"{code}: cca2 must be exactly two uppercase ASCII letters")
        rows.append({"code": code, "lat": point["lat"], "lon": point["lon"], "city": city})
    rows.sort(key=lambda row: row["code"])
    return rows


def go_string(value: str) -> str:
    """A double-quoted Go string literal for a JSON-safe value."""
    return json.dumps(value, ensure_ascii=False)


def go_float(value: float) -> str:
    """The shortest decimal that round-trips to the same float64."""
    return repr(value)


def render(rows: list[dict], date: str, digest: str) -> str:
    """The complete internal/view/colos.go for these rows."""
    lines = [
        "// Code generated by scripts/gen_colos.py; DO NOT EDIT.",
        "//",
        "// Source: " + SOURCE,
        "// Fetched: " + date,
        "// Entries: " + str(len(rows)),
        "// Input SHA-256: " + digest,
        "//",
        "// Cloudflare's public locations list, one row per IATA code, sorted by",
        "// code. It names where Cloudflare's edge is, not where a request went: a",
        "// colo drawn on a map is a reported label, never proof of a route.",
        "package view",
        "",
        'import "sort"',
        "",
        "// Colo is one of Cloudflare's locations: the IATA code its list names,",
        "// the coordinates of the airport, and the city Cloudflare gives it. A",
        "// city is empty for the few locations the list leaves unnamed.",
        "type Colo struct {",
        "\tCode string",
        "\tLat  float64",
        "\tLon  float64",
        "\tCity string",
        "}",
        "",
        "// colos is every location Cloudflare's list names, sorted by code.",
        "var colos = [...]Colo{",
    ]
    for row in rows:
        lines.append("\t{%s, %s, %s, %s}," % (
            go_string(row["code"]), go_float(row["lat"]), go_float(row["lon"]), go_string(row["city"])))
    lines += [
        "}",
        "",
        "// ColoAt looks up a location by its IATA code, exactly three uppercase",
        "// ASCII letters. The second result is false for a code the list does not",
        "// name, including a malformed one.",
        "func ColoAt(code string) (Colo, bool) {",
        "\ti := sort.Search(len(colos), func(i int) bool { return colos[i].Code >= code })",
        "\tif i < len(colos) && colos[i].Code == code {",
        "\t\treturn colos[i], true",
        "\t}",
        "\treturn Colo{}, false",
        "}",
        "",
        "// AllColos returns every known location, sorted by code. The returned",
        "// slice is the caller's: changing it does not change the table.",
        "func AllColos() []Colo { return append([]Colo(nil), colos[:]...) }",
        "",
    ]
    return "\n".join(lines)


def capture_date(path: Path) -> str | None:
    """The YYYY-MM-DD in a capture's file name, else None."""
    match = re.search(r"(\d{4}-\d{2}-\d{2})", path.name)
    return match.group(1) if match else None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Generate internal/view/colos.go from Cloudflare's public locations list.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="Without --input the list is fetched live; --check writes nothing.",
    )
    parser.add_argument("--input", type=Path, help="a saved locations JSON capture to read instead of fetching")
    parser.add_argument("--out", type=Path, default=OUT, help="the Go file to write or check")
    parser.add_argument("--date", help="the capture date to record (YYYY-MM-DD); default: today, or the input's file name")
    parser.add_argument("--check", action="store_true", help="write nothing and fail if --out differs")
    args = parser.parse_args(argv)

    if args.input:
        try:
            raw = args.input.read_bytes()
        except OSError as error:
            fail(f"reading {args.input}: {error}")
    else:
        raw = fetch(SOURCE)
    rows = parse(raw)
    if args.date:
        try:
            date = dt.date.fromisoformat(args.date).isoformat()
        except ValueError:
            fail(f"--date {args.date!r} is not YYYY-MM-DD")
    elif args.input and capture_date(args.input):
        date = capture_date(args.input)
    else:
        date = dt.date.today().isoformat()
    digest = hashlib.sha256(raw).hexdigest()
    text = render(rows, date, digest)

    if args.check:
        try:
            current = args.out.read_text(encoding="utf-8")
        except OSError as error:
            fail(f"reading {args.out}: {error}")
        if current != text:
            print(f"{args.out} is stale: run python3 scripts/gen_colos.py"
                  + (f" --input {args.input}" if args.input else ""), file=sys.stderr)
            return 1
        print(f"{args.out} is current ({len(rows)} locations, {date}, {digest})")
        return 0

    try:
        args.out.write_bytes(text.encode("utf-8"))
    except OSError as error:
        fail(f"writing {args.out}: {error}")
    print(f"wrote {args.out}: {len(rows)} locations, {date}, {digest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
