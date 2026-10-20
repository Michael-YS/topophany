"""The generated colo table stays what its snapshot and header say it is."""
import contextlib
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("gen_colos", Path(__file__).with_name("gen_colos.py"))
gen_colos = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gen_colos)

ROOT = gen_colos.ROOT
SNAPSHOT = ROOT / "internal/view/testdata/cloudflare-locations-2026-10-19.json"


class GeneratedColosTests(unittest.TestCase):
    def test_checked_in_table_is_current(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            status = gen_colos.main(["--check", "--input", str(SNAPSHOT)])
        self.assertEqual(status, 0, err.getvalue())
        self.assertIn("is current (309 locations, 2026-10-19", out.getvalue())

    def test_the_header_names_the_snapshot(self):
        raw = SNAPSHOT.read_bytes()
        text = (ROOT / "internal/view/colos.go").read_text(encoding="utf-8")
        digest = hashlib.sha256(raw).hexdigest()
        self.assertIn(f"// Input SHA-256: {digest}\n", text)
        self.assertIn("// Entries: 309\n", text)
        self.assertIn("// Source: https://speed.cloudflare.com/locations\n", text)
        rows = json.loads(raw)
        self.assertEqual(len(rows), 309)
        codes = [row["iata"] for row in rows]
        self.assertEqual(len(set(codes)), len(codes))

    def test_a_capture_reproduces_byte_for_byte(self):
        raw = SNAPSHOT.read_bytes()
        first = gen_colos.render(gen_colos.parse(raw), "2026-10-19", hashlib.sha256(raw).hexdigest())
        second = gen_colos.render(gen_colos.parse(raw), "2026-10-19", hashlib.sha256(raw).hexdigest())
        self.assertEqual(first, second)
        self.assertEqual(first, (ROOT / "internal/view/colos.go").read_text(encoding="utf-8"))

    def test_validation_rejects_a_broken_list(self):
        def row(**changes):
            base = {"iata": "NRT", "lat": 35.76, "lon": 140.38, "cca2": "JP", "region": "Asia Pacific", "city": "Tokyo"}
            base.update(changes)
            return base

        for name, rows in {
            "lowercase code": [row(iata="nrt")],
            "four-letter code": [row(iata="NRTT")],
            "duplicate code": [row(), row(city="Elsewhere")],
            "latitude out of range": [row(lat=91)],
            "longitude out of range": [row(lon=-181)],
            "non-finite latitude": [row(lat=float("inf"))],
            "control character in city": [row(city="To\x00kyo")],
            "country too long": [row(cca2="JPN")],
            "not an array": {"iata": "NRT"},
            "empty array": [],
        }.items():
            with self.subTest(name), self.assertRaises(SystemExit):
                gen_colos.parse(json.dumps(rows).encode())

        # The live list itself has three locations without a city, so an
        # empty city is valid even though a named one is the rule.
        self.assertEqual(gen_colos.parse(json.dumps([row(city="")]).encode())[0]["city"], "")

    def test_check_writes_nothing_and_reports_staleness(self):
        with tempfile.TemporaryDirectory() as temp:
            stale = Path(temp) / "colos.go"
            stale.write_text("// stale\n", encoding="utf-8")
            out, err = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                status = gen_colos.main(["--check", "--input", str(SNAPSHOT), "--out", str(stale)])
            self.assertEqual(status, 1)
            self.assertIn("stale", err.getvalue())
            self.assertEqual(stale.read_text(encoding="utf-8"), "// stale\n", "--check wrote a file")


if __name__ == "__main__":
    unittest.main()
