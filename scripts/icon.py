#!/usr/bin/env python3
"""Draw Topophany's globe: the favicon, the homepage's mark, the Apple touch
icon and the homepage's social card, from one dither.

The globe is the Earth seen from 20°N 140°E, lit from the upper left, in ink
on paper and an 8×8 ordered (Bayer) dither. By day the sea is a
checkerboard, the land is paper, and night falls across the terminator into
solid ink; the limb is drawn in ink. By night the same globe is light on
ink: the dots are its light, and the limb is the atmosphere, a ring of them.

It is topophany-bar's app icon, which that repository's scripts/make-icon.py
draws; the two draw it alike, and the favicon is its design/assets/mark.svg.

- site/public/favicon.svg: 16 cells, ink on a paper disc, and its light
  alone where the viewer prefers dark.
- site/public/img/mark.svg: the same 16 cells by day, for the homepage,
  which is always paper.
- site/public/apple-touch-icon.png: the app icon's globe in 60 cells of 3
  pixels, so every cell lands on whole pixels at the 180 the system asks for.
- site/public/og.png: the card a link to the homepage shows, 1200 × 630 in
  5-pixel cells: the globe over the 5×7 wordmark and its gloss, as the
  homepage's hero draws them (site/src/js/globe.js).

The land is internal/tui/land.bin: Natural Earth 1:110m land (public domain)
in 0.5° cells, row 0 at 90°N, column 0 at 180°W. The PNGs are written with
zlib alone, so the script needs nothing but Python.

Usage: python3 scripts/icon.py
"""
from __future__ import annotations

import math
from pathlib import Path
import struct
import sys
import zlib

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / "site" / "public"
LAND = (ROOT / "internal" / "tui" / "land.bin").read_bytes()
assert len(LAND) == 720 * 360 // 8, "land.bin should be 720 × 360 bits"

FACING = (20.0, 140.0)                     # what the globe faces, latitude and longitude
INK, PAPER, GLOW = "#1a1a1a", "#ffffff", "#f4f2ec"
BAYER = [0, 32, 8, 40, 2, 34, 10, 42, 48, 16, 56, 24, 50, 18, 58, 26,
         12, 44, 4, 36, 14, 46, 6, 38, 60, 28, 52, 20, 62, 30, 54, 22,
         3, 35, 11, 43, 1, 33, 9, 41, 51, 19, 59, 27, 49, 17, 57, 25,
         15, 47, 7, 39, 13, 45, 5, 37, 63, 31, 55, 23, 61, 29, 53, 21]


def unit(v):
    n = math.sqrt(sum(a * a for a in v))
    return tuple(a / n for a in v)


def smoothstep(e0, e1, x):
    t = min(1.0, max(0.0, (x - e0) / (e1 - e0)))
    return t * t * (3 - 2 * t)


def threshold(i, j):
    """The dither's threshold at cell column i, row j."""
    return (BAYER[(j & 7) * 8 + (i & 7)] + 0.5) / 64


LIGHT = unit((-0.55, 0.6, 0.58))           # from the left, above, and toward us
LAT0, LON0 = math.radians(FACING[0]), math.radians(FACING[1])


def is_land(lat, lon):
    l = (lon + 180) % 360
    i = min(359, max(0, int((90 - lat) * 2))) * 720 + min(719, int(l * 2))
    return LAND[i >> 3] & (0x80 >> (i & 7)) != 0


def geographic(x, y):
    """A point on the unit disc (y up) to latitude and longitude, or None off it."""
    r2 = x * x + y * y
    if r2 >= 1:
        return None
    z = math.sqrt(1 - r2)
    lat = math.asin(max(-1.0, min(1.0, z * math.sin(LAT0) + y * math.cos(LAT0))))
    lon = LON0 + math.atan2(x, z * math.cos(LAT0) - y * math.sin(LAT0))
    return math.degrees(lat), math.degrees(lon)


def globe(cells, radius, limb):
    """Each cell of a cells × cells grid holding a globe `radius` cells across
    its middle: (inside, ink by day, light by night). `limb` is where the
    limb's ink begins and ends, as 1 - z."""
    rows = []
    for j in range(cells):
        row = []
        for i in range(cells):
            x, y = (i + 0.5 - cells / 2) / radius, (cells / 2 - j - 0.5) / radius
            r = math.hypot(x, y)
            if r >= 1:
                row.append((False, False, False))
                continue
            z = math.sqrt(1 - r * r)
            # Land where most of the cell is.
            hits = sum(1 for v in range(4) for u in range(4)
                       if (g := geographic(x + (u - 1.5) / 4 / radius, y + (v - 1.5) / 4 / radius)) and is_land(*g))
            land = hits >= 8
            # By day the sea is a checkerboard and the land paper, each a
            # little darker away from the light; night falls across the
            # terminator into solid ink.
            nl = x * LIGHT[0] + y * LIGHT[1] + z * LIGHT[2]
            shade = 1 - max(0.0, nl)
            surface = 0.15 * shade if land else 0.5 + 0.1 * shade
            day = smoothstep(-0.12, 0.45, nl)
            surface = surface * day + (1 - day)
            # Ink draws the limb by day; by night it is a ring of light.
            rim = smoothstep(limb[0], limb[1], 1 - z)
            ink = max(surface, rim)
            glow = max(1 - ink, 0.6 * rim)
            t = threshold(i, j)
            row.append((True, ink > t, glow > t))
        rows.append(row)
    return rows


def path(rows, k, cell):
    """The cells where the k-th of each is set, as one path of runs, so they
    fill without seams."""
    d = []
    for j, row in enumerate(rows):
        i = 0
        while i < len(row):
            if not row[i][k]:
                i += 1
                continue
            start = i
            while i < len(row) and row[i][k]:
                i += 1
            d.append(f"M{start * cell:g} {j * cell:g}h{(i - start) * cell:g}v{cell:g}h-{(i - start) * cell:g}z")
    return "".join(d)


# MARK: the wordmark

# The 5×7 face of internal/tui's banner, as the homepage draws it.
FACE = {
    "T": ["#####", "..#..", "..#..", "..#..", "..#..", "..#..", "..#.."],
    "O": [".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."],
    "P": ["####.", "#...#", "#...#", "####.", "#....", "#....", "#...."],
    "H": ["#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"],
    "A": [".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"],
    "N": ["#...#", "##..#", "#.#.#", "#..##", "#...#", "#...#", "#...#"],
    "Y": ["#...#", "#...#", ".#.#.", "..#..", "..#..", "..#..", "..#.."],
    "E": ["#####", "#....", "#....", "####.", "#....", "#....", "#####"],
    "R": ["####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"],
    "C": [".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."],
    "F": ["#####", "#....", "#....", "####.", "#....", "#....", "#...."],
    "L": ["#....", "#....", "#....", "#....", "#....", "#....", "#####"],
}


def lettered(word, scale, advance=6):
    """The cells a word in the face fills, `scale` cells to its pixel, as a
    set of (column, row), and how wide it is. A letter takes `advance` of the
    face's pixels, and a space four."""
    cells, x = set(), 0
    for ch in word:
        if ch == " ":
            x += 4 * scale
            continue
        for fy, line in enumerate(FACE[ch]):
            for fx, c in enumerate(line):
                if c == "#":
                    cells |= {(x + fx * scale + u, fy * scale + v) for u in range(scale) for v in range(scale)}
        x += advance * scale
    return cells, x - (advance - 5) * scale


def wordmark(scale):
    """TOPOPHANY in solid ink, `scale` cells to the face's pixel, over a
    shadow in the dither's half tone. Returns the cells inked, relative to its
    top left, and how wide and tall they reach."""
    face, width = lettered("TOPOPHANY", scale)
    height = 7 * scale
    out = set(face)
    for x in range(width + scale):
        for y in range(height + scale):
            if (x, y) not in face and (x - scale + 1, y - scale + 1) in face and 0.5 > threshold(x, y):
                out.add((x, y))
    return out, width + scale, height + scale


# MARK: images

def png(width, rows):
    """A grayscale image, written with nothing but zlib."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    raw = b"".join(b"\x00" + row for row in rows)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, len(rows), 8, 0, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


def pixels(inked, columns, lines, cell):
    """Rows of a columns × lines grid of cells, each `cell` pixels square,
    in ink where inked and paper elsewhere."""
    ink, paper = int(INK[1:3], 16), int(PAPER[1:3], 16)
    rows = []
    for j in range(lines):
        row = bytearray()
        for i in range(columns):
            row += bytes([ink if (i, j) in inked else paper]) * cell
        rows += [bytes(row)] * cell
    return rows


def inked(rows, at=(0, 0)):
    """The cells a globe inks by day, moved to `at`."""
    return {(at[0] + i, at[1] + j) for j, row in enumerate(rows) for i, c in enumerate(row) if c[1]}


def write(relative, data):
    out = ROOT / relative
    out.parent.mkdir(parents=True, exist_ok=True)
    if isinstance(data, str):
        out.write_text(data, encoding="utf-8")
    else:
        out.write_bytes(data)
    print(f"wrote {relative}")


def main():
    mark = globe(16, 8, (0.3, 0.6))
    write("site/public/favicon.svg",
          '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" shape-rendering="crispEdges">'
          "<style>.night{display:none}@media(prefers-color-scheme:dark){.day{display:none}.night{display:inline}}</style>"
          f'<g class="day"><path fill="{PAPER}" d="{path(mark, 0, 1)}"/><path fill="{INK}" d="{path(mark, 1, 1)}"/></g>'
          f'<path class="night" fill="{GLOW}" d="{path(mark, 2, 1)}"/></svg>\n')
    write("site/public/img/mark.svg",
          '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" shape-rendering="crispEdges">'
          f'<path fill="{PAPER}" d="{path(mark, 0, 1)}"/><path fill="{INK}" d="{path(mark, 1, 1)}"/></svg>\n')

    # The app icon's globe is 22 of its 64 cells in radius.
    write("site/public/apple-touch-icon.png", png(180, pixels(inked(globe(60, 22 * 60 / 64, (0.45, 0.85))), 60, 60, 3)))

    # The card: 240 × 126 cells, the globe over the wordmark over its gloss.
    columns, lines = 240, 126
    word, word_width, word_height = wordmark(3)
    # The gloss is spaced out to about the word's width, as the banner sets it.
    gloss, gloss_width = lettered("THE APPEARANCE OF A PLACE", 1, 7)
    radius, top = 30, 10
    card = inked(globe(2 * radius + 2, radius, (0.45, 0.85)), ((columns - 2 * radius - 2) // 2, top))
    below = top + 2 * radius + 2 + 8
    card |= {(x + (columns - word_width) // 2, y + below) for x, y in word}
    card |= {(x + (columns - gloss_width) // 2, y + below + word_height + 5) for x, y in gloss}
    write("site/public/og.png", png(columns * 5, pixels(card, columns, lines, 5)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
