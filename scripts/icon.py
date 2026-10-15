#!/usr/bin/env python3
"""Draw the Topophany mark: the globe of the homepage and the README banner in
little, a dot-matrix Earth lit from the upper left with an arc of light from
Shanghai to a place it brings to light across the Pacific.

The land is internal/tui/land.bin, seen from 20N 140E. The mark is written as
site/public/favicon.svg, which the homepage also shows beside its name, and
painted on the page's ink as the 180-pixel site/public/apple-touch-icon.png
by a headless Chromium (see scripts/capture.py).
Usage: python3 scripts/icon.py [--browser PATH]
"""
from __future__ import annotations

import argparse
import base64
import math
from pathlib import Path
import subprocess
import sys
import tempfile

from capture import browser

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / "site" / "public"
LAND = (ROOT / "internal" / "tui" / "land.bin").read_bytes()

SIZE = 64                  # the mark's square, in its own units
RADIUS = 30.5              # the globe's
PITCH = 1.8                # between dots of land
CENTRE = (20.0, 140.0)     # what the globe faces, latitude and longitude
LIGHT = (-0.55, 0.6, 0.58) # where the light comes from: left, up, toward us
ARC = ((31.2, 121.5), (21.3, 202.1), 0.36)  # Shanghai to Honolulu, and how far the arc bows
INK, SEA, LIMB = "#0a1016", ("#2f7ea3", "#15405a", "#0b1a24"), "#7dd3fc"
SHORE, LIT, BEAM, CITY = "#1f6f68", "#8ff5e2", ("#5eead4", "#e6fffb"), "#fcd34d"


def land(lat: float, lon: float) -> bool:
    col = min(719, int((lon + 180) % 360 * 2))
    row = min(359, max(0, int((90 - lat) * 2)))
    i = row * 720 + col
    return LAND[i >> 3] & (0x80 >> (i & 7)) != 0


def mix(a: str, b: str, t: float) -> str:
    x, y = (bytes.fromhex(c[1:]) for c in (a, b))
    return "#" + "".join(f"{round(p + (q - p) * t):02x}" for p, q in zip(x, y))


def mark() -> str:
    c, lat0, lon0 = SIZE / 2, *map(math.radians, CENTRE)
    norm = math.hypot(*LIGHT)
    light = [v / norm for v in LIGHT]
    parts = [f'<defs><radialGradient id="sea" cx=".36" cy=".32" r=".78">'
             f'<stop offset="0" stop-color="{SEA[0]}"/><stop offset=".5" stop-color="{SEA[1]}"/>'
             f'<stop offset="1" stop-color="{SEA[2]}"/></radialGradient></defs>',
             f'<circle cx="{c:g}" cy="{c:g}" r="{RADIUS:g}" fill="url(#sea)"/>']
    # Land as a grid of dots, each the colour and size of the light on it.
    n = int(RADIUS / PITCH) + 1
    for j in range(-n, n + 1):
        for i in range(-n, n + 1):
            x, y = (i + 0.5) * PITCH, (j + 0.5) * PITCH
            if math.hypot(x, y) > RADIUS - PITCH * 0.45:
                continue
            X, Y = x / RADIUS, -y / RADIUS
            Z = math.sqrt(max(0.0, 1 - X * X - Y * Y))
            lat = math.asin(Z * math.sin(lat0) + Y * math.cos(lat0))
            lon = lon0 + math.atan2(X, Z * math.cos(lat0) - Y * math.sin(lat0))
            if not land(math.degrees(lat), math.degrees(lon)):
                continue
            shade = min(1.0, 0.15 + max(0.0, X * light[0] + Y * light[1] + Z * light[2]))
            parts.append(f'<circle cx="{c + x:.2f}" cy="{c + y:.2f}" r="{PITCH * 0.58 * (0.7 + 0.3 * Z):.2f}" '
                         f'fill="{mix(SHORE, LIT, shade ** 1.3)}"/>')
    parts.append(f'<circle cx="{c:g}" cy="{c:g}" r="{RADIUS - 0.4:g}" fill="none" stroke="{LIMB}" '
                 f'stroke-opacity=".5" stroke-width="1.1"/>')

    def seen(lat: float, lon: float) -> tuple[float, float]:
        lat, lon = math.radians(lat), math.radians(lon)
        x = math.cos(lat) * math.sin(lon - lon0)
        y = math.cos(lat0) * math.sin(lat) - math.sin(lat0) * math.cos(lat) * math.cos(lon - lon0)
        return c + x * RADIUS, c - y * RADIUS

    # The arc bows up from the chord between its places, a glow under a core.
    a, b, bow = ARC
    p, q = seen(*a), seen(*b)
    length = math.dist(p, q)
    nx, ny = (q[1] - p[1]) / length, (p[0] - q[0]) / length
    if ny > 0:
        nx, ny = -nx, -ny
    control = ((p[0] + q[0]) / 2 + nx * length * bow, (p[1] + q[1]) / 2 + ny * length * bow)
    d = f"M{p[0]:.2f} {p[1]:.2f}Q{control[0]:.2f} {control[1]:.2f} {q[0]:.2f} {q[1]:.2f}"
    parts += [f'<path d="{d}" fill="none" stroke="{BEAM[0]}" stroke-opacity=".38" stroke-width="6.72" stroke-linecap="round"/>',
              f'<path d="{d}" fill="none" stroke="{BEAM[1]}" stroke-width="2.4" stroke-linecap="round"/>',
              f'<circle cx="{p[0]:.2f}" cy="{p[1]:.2f}" r="3.6" fill="{INK}"/>',
              f'<circle cx="{p[0]:.2f}" cy="{p[1]:.2f}" r="2.6" fill="{CITY}"/>',
              f'<circle cx="{q[0]:.2f}" cy="{q[1]:.2f}" r="6.4" fill="{CITY}" fill-opacity=".22"/>',
              f'<circle cx="{q[0]:.2f}" cy="{q[1]:.2f}" r="3.4" fill="{CITY}"/>']
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {SIZE} {SIZE}">\n'
            + "\n".join(parts) + "\n</svg>\n")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--browser", help="Chromium to paint with (default: $CHROME, or one on PATH)")
    args = parser.parse_args()
    svg = mark()
    (SITE / "favicon.svg").write_text(svg, encoding="utf-8")
    print("wrote site/public/favicon.svg")
    # iOS rounds the corners itself, so the ink fills the square.
    page = (f'<!doctype html><html><body style="margin:0;background:{INK}">'
            f'<img src="data:image/svg+xml;base64,{base64.b64encode(svg.encode()).decode()}" '
            'style="position:absolute;left:18px;top:18px;width:144px;height:144px"></body></html>')
    with tempfile.TemporaryDirectory() as temp:
        html = Path(temp) / "icon.html"
        html.write_text(page, encoding="utf-8")
        out = SITE / "apple-touch-icon.png"
        subprocess.run([browser(args.browser), "--headless", "--no-sandbox", "--hide-scrollbars",
                        "--force-device-scale-factor=1", "--window-size=180,180", f"--screenshot={out}",
                        html.as_uri()], check=True, capture_output=True)
    print("wrote site/public/apple-touch-icon.png")
    return 0


if __name__ == "__main__":
    sys.exit(main())
