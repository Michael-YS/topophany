#!/usr/bin/env python3
"""Capture the TUI stills in docs/assets, one per language where the README shows them.

The TUI records each still as one frame (TestCaptures in internal/tui), and the
homepage's terminal renderer (site/public/js/term.js) paints it in a headless
Chromium, with its CSS choosing the CJK fonts for the language, so a capture
looks as the homepage's replay does: 10x20 pixels a cell.

Japanese wants a font with Japanese glyph forms (Noto Sans JP, Hiragino, Yu
Gothic); where the system has none, --fonts adds a directory of fonts for this
run only. Usage: python3 scripts/capture.py [--browser PATH] [--fonts DIR]
"""
from __future__ import annotations

import argparse
import functools
import glob
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "docs" / "assets"
SITE = ROOT / "site" / "public"
CELL = (10, 20)  # pixels per cell, as the homepage draws them

PAGE = """<!doctype html>
<html lang="{lang}">
<head>
<meta charset="utf-8">
<link rel="stylesheet" href="site.css">
<style>html, body {{ margin: 0; background: #0a1016; overflow: hidden; }} canvas {{ width: {width}px; }}</style>
</head>
<body>
<canvas></canvas>
<script type="module">
import {{ Screen }} from './term.js';
const data = await (await fetch('{frame}')).json();
await document.fonts.ready;
const screen = new Screen(document.querySelector('canvas'), data.size[0], data.size[1], data.styles);
screen.apply(data.chapters[0].frames[0].ops);
screen.paint();
</script>
</body>
</html>
"""


def browser(given: str | None) -> str:
    """A Chromium that can take screenshots headless."""
    for candidate in [given, os.environ.get("CHROME"), "chromium", "chromium-browser", "google-chrome",
                      "google-chrome-stable", "chrome-headless-shell"]:
        if candidate and shutil.which(candidate):
            return shutil.which(candidate)
    # Playwright's headless shell, if one is installed.
    found = sorted(glob.glob(os.path.expanduser(
        "~/.cache/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-*/chrome-headless-shell")))
    if found:
        return found[-1]
    raise SystemExit("capture: no Chromium found; pass --browser or set CHROME")


class Quiet(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def serve(directory: Path) -> http.server.ThreadingHTTPServer:
    handler = functools.partial(Quiet, directory=str(directory))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--browser", help="Chromium to paint with (default: $CHROME, or one on PATH)")
    parser.add_argument("--fonts", type=Path, help="a directory of fonts to add for this run")
    args = parser.parse_args()
    chrome = browser(args.browser)
    with tempfile.TemporaryDirectory() as temp:
        work = Path(temp)
        frames = work / "frames"
        frames.mkdir()
        subprocess.run(["go", "test", "-count=1", "-run", "^TestCaptures$", "./internal/tui"], cwd=ROOT, check=True,
                       env={**os.environ, "TOPO_CAPTURES": str(frames)})
        for name in ("js/term.js", "css/site.css"):
            shutil.copy(SITE / name, frames / Path(name).name)
        env = dict(os.environ)
        if args.fonts:
            conf = work / "fonts.conf"
            conf.write_text('<?xml version="1.0"?>\n<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n<fontconfig>\n'
                            '  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>\n'
                            f"  <dir>{args.fonts.resolve()}</dir>\n  <cachedir>{work / 'fontcache'}</cachedir>\n"
                            "</fontconfig>\n")
            env["FONTCONFIG_FILE"] = str(conf)
        server = serve(frames)
        try:
            for frame in sorted(frames.glob("*.*.json")):
                name, lang = frame.name.split(".")[:2]
                cols, rows = json.loads(frame.read_text())["size"]
                width, height = cols * CELL[0], rows * CELL[1]
                page = frames / f"{name}.{lang}.html"
                page.write_text(PAGE.format(lang=lang, width=width, frame=frame.name), encoding="utf-8")
                out = ASSETS / f"{name}.{lang}.png"
                subprocess.run([chrome, "--headless", "--no-sandbox", "--hide-scrollbars", "--force-device-scale-factor=1",
                                f"--window-size={width},{height}", "--virtual-time-budget=5000",
                                f"--screenshot={out}", f"http://127.0.0.1:{server.server_port}/{page.name}"],
                               env=env, check=True, capture_output=True)
                print(f"wrote {out.relative_to(ROOT)}")
        finally:
            server.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
