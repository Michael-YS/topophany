#!/usr/bin/env python3
"""Capture the TUI stills and animations in docs/assets, one per language where the README shows them.

The TUI records each capture in the tour's format (TestCaptures in internal/tui):
a still as one frame, an animation as chapters of the homepage's tour. The
homepage's terminal renderer (site/src/js/term.js) paints them in a headless
Chromium, with its CSS choosing the CJK fonts for the language, and the page
posts every frame back as a PNG, so a capture looks as the homepage's replay
does: 10x20 pixels a cell. ffmpeg writes a still as it was painted
(<name>.<language>.png) and makes an animation's frames a looping GIF
(<name>.<language>.gif), with one palette for all of them and each held as
long as the tour holds it.

Japanese wants a font with Japanese glyph forms (Noto Sans JP, Hiragino, Yu
Gothic); where the system has none, --fonts adds a directory of fonts for this
run only. Usage: python3 scripts/capture.py [--browser PATH] [--ffmpeg PATH] [--fonts DIR]
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
import time

ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "docs" / "assets"
SITE = ROOT / "site" / "src"
CELL = (10, 20)  # pixels per cell, as the homepage draws them
PATIENCE = 120  # seconds a capture may take to paint

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
const post = (path, body) => fetch(path, {{ method: 'POST', body }});
try {{
  const data = await (await fetch('{capture}')).json();
  await document.fonts.ready;
  const canvas = document.querySelector('canvas');
  const screen = new Screen(canvas, data.size[0], data.size[1], data.styles);
  const frames = data.chapters.flatMap((chapter) => chapter.frames);
  for (const [i, frame] of frames.entries()) {{
    screen.apply(frame.ops);
    screen.paint();
    await post(`frame/${{i}}`, await new Promise((done) => canvas.toBlob(done, 'image/png')));
  }}
  await post('done', JSON.stringify(frames.map((frame) => frame.d)));
}} catch (e) {{
  await post('error', String(e.stack || e));
}}
</script>
</body>
</html>
"""


def browser(given: str | None) -> str:
    """A Chromium that can paint headless."""
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


def encoder(given: str | None) -> str:
    """An ffmpeg to make the animations with."""
    for candidate in [given, os.environ.get("FFMPEG"), "ffmpeg"]:
        if candidate and shutil.which(candidate):
            return shutil.which(candidate)
    raise SystemExit("capture: no ffmpeg found; pass --ffmpeg or set FFMPEG")


class Inbox:
    """What the page posts back while it paints one capture: each frame as a
    PNG in directory, then how long each is held, or what went wrong."""

    def __init__(self, directory: Path):
        self.directory = directory
        self.held: list[int] | None = None
        self.error: str | None = None
        self.done = threading.Event()

    def post(self, path: str, body: bytes) -> None:
        kind, _, index = path.lstrip("/").partition("/")
        if kind == "frame":
            (self.directory / f"{int(index):04d}.png").write_bytes(body)
        elif kind == "done":
            self.held = json.loads(body)
            self.done.set()
        elif kind == "error":
            self.error = body.decode(errors="replace")
            self.done.set()


class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        self.server.inbox.post(self.path, self.rfile.read(int(self.headers.get("Content-Length", 0))))
        self.send_response(204)
        self.end_headers()


def serve(directory: Path) -> http.server.ThreadingHTTPServer:
    handler = functools.partial(Handler, directory=str(directory))
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def paint(chrome: str, env: dict, server, page: str, size: tuple[int, int], frames: Path) -> list[int]:
    """Paints the capture page shows into frames and returns how long each frame is held."""
    inbox = server.inbox = Inbox(frames)
    log = frames / "chromium.log"
    with log.open("wb") as err:
        proc = subprocess.Popen([chrome, "--headless", "--no-sandbox", "--hide-scrollbars", "--force-device-scale-factor=1",
                                 f"--window-size={size[0]},{size[1]}", f"http://127.0.0.1:{server.server_port}/{page}"],
                                env=env, stdout=subprocess.DEVNULL, stderr=err)
        try:
            deadline = time.monotonic() + PATIENCE
            while not inbox.done.wait(0.1):
                if proc.poll() is not None:
                    raise SystemExit(f"capture: Chromium quit painting {page}:\n{log.read_text(errors='replace')}")
                if time.monotonic() > deadline:
                    raise SystemExit(f"capture: {page} was not painted in {PATIENCE} s")
        finally:
            proc.kill()
            proc.wait()
    if inbox.error:
        raise SystemExit(f"capture: {page}: {inbox.error}")
    return inbox.held


def keep(ffmpeg: str, frame: Path, out: Path) -> None:
    """Writes a painted frame as an RGB PNG: the same pixels as the page's
    RGBA one, in a quarter less than a screenshot of it takes."""
    subprocess.run([ffmpeg, "-v", "error", "-y", "-i", str(frame), "-pix_fmt", "rgb24", "-pred", "none",
                    "-compression_level", "9", str(out)], check=True)


def animate(ffmpeg: str, frames: Path, held: list[int], out: Path) -> None:
    """Makes frames a looping GIF, each frame held for its milliseconds in held."""
    listing = frames / "frames.txt"
    # Each frame read at 100 fps, so its time is kept to the GIF's
    # centisecond rather than rounded to an image's 25 fps.
    listing.write_text("ffconcat version 1.0\n" + "".join(
        f"file {i:04d}.png\noption framerate 100\nduration {ms / 1000:.3f}\n" for i, ms in enumerate(held)))
    # One palette from every frame; no dithering, so text keeps its edges.
    subprocess.run([ffmpeg, "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", str(listing),
                    "-vf", "split[a][b];[a]palettegen=stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle",
                    "-fps_mode", "passthrough", "-final_delay", str(round(held[-1] / 10)), "-loop", "0", str(out)],
                   check=True)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--browser", help="Chromium to paint with (default: $CHROME, or one on PATH)")
    parser.add_argument("--ffmpeg", help="ffmpeg to make the animations with (default: $FFMPEG, or one on PATH)")
    parser.add_argument("--fonts", type=Path, help="a directory of fonts to add for this run")
    args = parser.parse_args()
    chrome, ffmpeg = browser(args.browser), encoder(args.ffmpeg)
    with tempfile.TemporaryDirectory() as temp:
        work = Path(temp)
        captures = work / "captures"
        captures.mkdir()
        subprocess.run(["go", "test", "-count=1", "-run", "^TestCaptures$", "./internal/tui"], cwd=ROOT, check=True,
                       env={**os.environ, "TOPO_CAPTURES": str(captures)})
        for name in ("js/term.js", "css/site.css"):
            shutil.copy(SITE / name, captures / Path(name).name)
        env = dict(os.environ)
        if args.fonts:
            conf = work / "fonts.conf"
            conf.write_text('<?xml version="1.0"?>\n<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n<fontconfig>\n'
                            '  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>\n'
                            f"  <dir>{args.fonts.resolve()}</dir>\n  <cachedir>{work / 'fontcache'}</cachedir>\n"
                            "</fontconfig>\n")
            env["FONTCONFIG_FILE"] = str(conf)
        server = serve(captures)
        try:
            for capture in sorted(captures.glob("*.*.json")):
                name, lang = capture.name.split(".")[:2]
                cols, rows = json.loads(capture.read_text())["size"]
                width, height = cols * CELL[0], rows * CELL[1]
                page = f"{name}.{lang}.html"
                (captures / page).write_text(PAGE.format(lang=lang, width=width, capture=capture.name), encoding="utf-8")
                frames = work / "frames" / f"{name}.{lang}"
                frames.mkdir(parents=True)
                held = paint(chrome, env, server, page, (width, height), frames)
                if len(held) == 1:
                    out = ASSETS / f"{name}.{lang}.png"
                    keep(ffmpeg, frames / "0000.png", out)
                else:
                    out = ASSETS / f"{name}.{lang}.gif"
                    animate(ffmpeg, frames, held, out)
                print(f"wrote {out.relative_to(ROOT)}")
        finally:
            server.shutdown()
    return 0


if __name__ == "__main__":
    sys.exit(main())
