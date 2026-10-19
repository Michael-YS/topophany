#!/usr/bin/env python3
"""Draw the Topophany mark: a dot-matrix Earth lit from the upper left, its
land a halftone of the light on it, with an arc of light from Shanghai to
Honolulu, the place it brings to light across the Pacific at dusk.

The land is internal/tui/land.bin, seen from 20N 140E. The mark is drawn twice:

- small, as site/public/favicon.svg, which the homepage also shows beside its
  name: coarse dots and a broad arc, so the land and the light still read at
  16 pixels;
- whole, as the 180-pixel site/public/apple-touch-icon.png: fine dots on a
  shaded sea and ground, in an atmosphere among a few stars, on the page's
  ink. A headless Chromium paints it (see scripts/capture.py).

Usage: python3 scripts/icon.py [--browser PATH]
"""
from __future__ import annotations

import argparse
import base64
import math
from pathlib import Path
import struct
import subprocess
import sys
import tempfile
import zlib

from capture import browser

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / "site" / "public"
LAND = (ROOT / "internal" / "tui" / "land.bin").read_bytes()

CENTRE = (20.0, 140.0)                     # what the globe faces, latitude and longitude
SHANGHAI, HONOLULU = (31.2, 121.5), (21.3, 202.1)
BOW = 0.36                                 # how far the arc bows, of its chord

INK = "#0a1016"
ACCENT, ACCENT2, GLOW = "#5eead4", "#7dd3fc", "#f0fdfa"
LAND_DAY, LAND_DUSK, LAND_DARK = "#b4f5e8", "#2f9389", "#1a4448"
SEA_NIGHT, SEA_DAY, SEA_GLINT = "#0e1d29", "#22628a", "#4c93b8"
GROUND_DARK, GROUND_DAY = "#0e2321", "#3f9682"
CITY, EMBER = "#fcd34d", "#f59e0b"
SKY = ("#121d26", "#0a1016")               # the touch icon's ink, top to bottom


def unit(v: tuple[float, float, float]) -> tuple[float, float, float]:
    n = math.hypot(*v)
    return v[0] / n, v[1] / n, v[2] / n


LIGHT = unit((-0.55, 0.6, 0.58))           # from the left, above and toward us
HALFWAY = unit((LIGHT[0], LIGHT[1], LIGHT[2] + 1))
LAT0, LON0 = map(math.radians, CENTRE)


def clamp(v: float, lo: float = 0.0, hi: float = 1.0) -> float:
    return lo if v < lo else hi if v > hi else v


def smoothstep(e0: float, e1: float, x: float) -> float:
    t = clamp((x - e0) / (e1 - e0))
    return t * t * (3 - 2 * t)


def rgb(c: str) -> tuple[float, float, float]:
    return tuple(b / 255 for b in bytes.fromhex(c[1:]))


def mix(a, b, t: float):
    """Mixes two colours, given as "#rrggbb" or as fractions, into fractions."""
    a, b = (rgb(c) if isinstance(c, str) else c for c in (a, b))
    t = clamp(t)
    return tuple(p + (q - p) * t for p, q in zip(a, b))


def css(c) -> str:
    c = rgb(c) if isinstance(c, str) else c
    return "#" + "".join(f"{round(clamp(v) * 255):02x}" for v in c)


def land(lat: float, lon: float) -> bool:
    col = min(719, int((lon + 180) % 360 * 2))
    row = min(359, max(0, int((90 - lat) * 2)))
    i = row * 720 + col
    return LAND[i >> 3] & (0x80 >> (i & 7)) != 0


# On the globe's disc, x runs right and y up, both in radii; z comes toward us.
def depth(x: float, y: float) -> float:
    return math.sqrt(max(0.0, 1 - x * x - y * y))


def geographic(x: float, y: float) -> tuple[float, float]:
    z = depth(x, y)
    lat = math.asin(clamp(z * math.sin(LAT0) + y * math.cos(LAT0), -1, 1))
    lon = LON0 + math.atan2(x, z * math.cos(LAT0) - y * math.sin(LAT0))
    return math.degrees(lat), math.degrees(lon)


def seen(lat: float, lon: float) -> tuple[float, float]:
    lat, lon = math.radians(lat), math.radians(lon)
    x = math.cos(lat) * math.sin(lon - LON0)
    y = math.cos(LAT0) * math.sin(lat) - math.sin(LAT0) * math.cos(lat) * math.cos(lon - LON0)
    return x, y


def facing(x: float, y: float) -> float:
    """How squarely a point of the surface faces the light, -1 to 1."""
    return x * LIGHT[0] + y * LIGHT[1] + depth(x, y) * LIGHT[2]


def daylight(x: float, y: float) -> float:
    """How much light falls on a point: a little at night, so the globe keeps
    its shape, up to 1 facing the sun."""
    nl = facing(x, y)
    return (0.14 + 0.86 * smoothstep(-0.15, 0.35, nl) * (0.45 + 0.55 * max(0.0, nl))) * depth(x, y) ** 0.3


def sunward(x: float, y: float) -> float:
    r = math.hypot(x, y)
    return max(0.0, (x * LIGHT[0] + y * LIGHT[1]) / r) if r > 0 else 0.0


A, B = seen(*SHANGHAI), seen(*HONOLULU)


def warmth(x: float, y: float) -> float:
    """Honolulu's glow, on the sea and the land around it."""
    d = math.hypot(x - B[0], y - B[1]) / 0.4076
    return math.exp(-2.5 * d * d)


class Picture:
    """The globe placed in a square of `size` units: its radius and centre,
    and the pitch of the land's dots."""

    def __init__(self, size: float, radius: float, centre: tuple[float, float], pitch: float, beam: float):
        self.size, self.radius, self.cx, self.cy, self.pitch, self.beam = size, radius, *centre, pitch, beam
        self.a, self.b = self.point(A), self.point(B)
        # The arc bows up from the chord between its places, as it always has.
        length = math.dist(self.a, self.b)
        nx, ny = (self.b[1] - self.a[1]) / length, (self.a[0] - self.b[0]) / length
        if ny > 0:
            nx, ny = -nx, -ny
        self.control = ((self.a[0] + self.b[0]) / 2 + nx * length * BOW, (self.a[1] + self.b[1]) / 2 + ny * length * BOW)
        self.samples = [self.arc(k / 400) for k in range(401)]

    def point(self, p: tuple[float, float]) -> tuple[float, float]:
        return self.cx + p[0] * self.radius, self.cy - p[1] * self.radius

    def disc(self, x: float, y: float) -> tuple[float, float]:
        return (x - self.cx) / self.radius, (self.cy - y) / self.radius

    def arc(self, u: float) -> tuple[float, float]:
        (ax, ay), (cx, cy), (bx, by) = self.a, self.control, self.b
        m = 1 - u
        return m * m * ax + 2 * m * u * cx + u * u * bx, m * m * ay + 2 * m * u * cy + u * u * by

    def width(self, u: float) -> float:
        """The arc thins toward Shanghai, so it reads as light setting out."""
        return self.beam * (0.3 + 0.7 * u ** 0.8)

    def ribbon(self, scale: float = 1.0) -> str:
        """The arc as one closed shape, with round ends."""
        left, right = [], []
        for k in range(121):
            u = k / 120
            p, q = self.arc(max(0.0, u - 1e-3)), self.arc(min(1.0, u + 1e-3))
            dx, dy = q[0] - p[0], q[1] - p[1]
            n = math.hypot(dx, dy)
            x, y = self.arc(u)
            w = self.width(u) * scale / 2
            left.append((x - dy / n * w, y + dx / n * w))
            right.append((x + dy / n * w, y - dx / n * w))
        pts = left + right[::-1]
        d = "M" + " ".join(f"{x:.2f} {y:.2f}" for x, y in pts) + "Z"
        for u in (0.0, 1.0):
            (x, y), r = self.arc(u), self.width(u) * scale / 2
            d += f"M{x - r:.2f} {y:.2f}a{r:.2f} {r:.2f} 0 1 0 {2 * r:.2f} 0a{r:.2f} {r:.2f} 0 1 0 {-2 * r:.2f} 0Z"
        return d

    def cells(self) -> dict[tuple[int, int], float]:
        """How much of each cell of the dots' grid is land."""
        n = int(math.ceil(self.radius / self.pitch)) + 2
        share = {}
        for j in range(-n, n):
            for i in range(-n, n):
                hits = 0
                for v in (-1, 0, 1):
                    for u in (-1, 0, 1):
                        x, y = self.disc(self.cx + (i + 0.5 + u / 3) * self.pitch, self.cy + (j + 0.5 + v / 3) * self.pitch)
                        if x * x + y * y < 1 and land(*geographic(x, y)):
                            hits += 1
                share[i, j] = hits / 9
        return share

    def dots(self, size: tuple[float, float], clearing: float, keepout: float) -> list[str]:
        """The land as a halftone: each cell's dot grows with the light on it.
        Shanghai's light and the arc keep the land clear around them."""
        out = []
        for (i, j), share in sorted(self.cells().items(), key=lambda c: (c[0][1], c[0][0])):
            if share < 0.5:
                continue
            px, py = self.cx + (i + 0.5) * self.pitch, self.cy + (j + 0.5) * self.pitch
            x, y = self.disc(px, py)
            if math.hypot(x, y) * self.radius > self.radius - 0.6 * self.pitch:
                continue
            if math.dist((px, py), self.a) < clearing:
                continue
            d, k = min((math.dist((px, py), s), k) for k, s in enumerate(self.samples))
            if d < keepout * (0.5 + 0.5 * k / 400) + self.beam / 2:
                continue
            z, w = depth(x, y), warmth(x, y)
            tone = clamp(clamp(1.5 * max(0.0, facing(x, y))) + 0.6 * w)
            lit = max(tone, 0.18)
            c = mix(LAND_DUSK, LAND_DAY, ((lit - 0.33) / 0.67) ** 0.8) if lit > 0.33 else mix(LAND_DARK, LAND_DUSK, lit * 3)
            c = mix(mix(INK, c, 0.5 + 0.5 * math.sqrt(z)), mix(CITY, GLOW, 0.3), 0.5 * w)
            r = self.pitch * (size[0] + size[1] * tone ** 0.8) * (0.75 + 0.25 * math.sqrt(z))
            out.append(f'<circle cx="{px:.2f}" cy="{py:.2f}" r="{r:.2f}" fill="{css(c)}"/>')
        return out


def favicon() -> str:
    """The small mark: no stars or haze, so it sits on any page or tab."""
    p = Picture(64, 30.5, (32, 32), pitch=1.8, beam=2.9)
    c, r = 32, 30.5
    # The sea's light falls off from under the sun, so a radial gradient from
    # there, sampled from the light itself, shades it.
    sun = p.point((LIGHT[0], LIGHT[1]))
    reach = math.dist(sun, (c, c)) + r
    stops = []
    for k in range(9):
        t = k / 8
        x, y = p.disc(sun[0] + (c - sun[0]) / math.dist(sun, (c, c)) * reach * t,
                      sun[1] + (c - sun[1]) / math.dist(sun, (c, c)) * reach * t)
        s = min(1.0, math.hypot(x, y))
        if s > 0.999:
            x, y = x / s * 0.999, y / s * 0.999
        stops.append(f'<stop offset="{t:.3f}" stop-color="{css(mix(SEA_NIGHT, SEA_DAY, daylight(x, y)))}"/>')
    lit_limb = css(mix(ACCENT2, GLOW, 0.35))
    defs = (f'<defs><radialGradient id="sea" gradientUnits="userSpaceOnUse" cx="{sun[0]:.2f}" cy="{sun[1]:.2f}" r="{reach:.2f}">'
            + "".join(stops) + '</radialGradient>'
            f'<radialGradient id="dusk" gradientUnits="userSpaceOnUse" cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="{0.42 * r:.2f}">'
            f'<stop offset="0" stop-color="{css(mix(EMBER, CITY, 0.5))}" stop-opacity=".32"/>'
            f'<stop offset="1" stop-color="{css(mix(EMBER, CITY, 0.5))}" stop-opacity="0"/></radialGradient>'
            f'<linearGradient id="limb" gradientUnits="userSpaceOnUse" x1="{c - r * 0.7:.2f}" y1="{c - r * 0.7:.2f}" x2="{c + r * 0.7:.2f}" y2="{c + r * 0.7:.2f}">'
            f'<stop offset="0" stop-color="{lit_limb}" stop-opacity=".8"/><stop offset=".55" stop-color="{ACCENT2}" stop-opacity=".35"/>'
            f'<stop offset="1" stop-color="{ACCENT2}" stop-opacity=".22"/></linearGradient>'
            f'<linearGradient id="beam" gradientUnits="userSpaceOnUse" x1="{p.a[0]:.2f}" y1="{p.a[1]:.2f}" x2="{p.b[0]:.2f}" y2="{p.b[1]:.2f}">'
            f'<stop offset="0" stop-color="{css(mix(ACCENT, mix(GLOW, ACCENT, 0.12), 0.45))}" stop-opacity=".9"/>'
            f'<stop offset="1" stop-color="{css(mix(GLOW, ACCENT, 0.12))}"/></linearGradient>'
            f'<radialGradient id="halo"><stop offset="0" stop-color="{css(mix(CITY, EMBER, 0.3))}" stop-opacity=".75"/>'
            f'<stop offset=".45" stop-color="{css(mix(CITY, EMBER, 0.3))}" stop-opacity=".25"/>'
            f'<stop offset="1" stop-color="{css(mix(CITY, EMBER, 0.3))}" stop-opacity="0"/></radialGradient>'
            f'<clipPath id="globe"><circle cx="{c}" cy="{c}" r="{r}"/></clipPath></defs>')
    parts = [defs, f'<circle cx="{c}" cy="{c}" r="{r}" fill="url(#sea)"/>',
             f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="{0.42 * r:.2f}" fill="url(#dusk)" clip-path="url(#globe)"/>']
    parts += p.dots(size=(0.30, 0.22), clearing=2.4, keepout=0.5)
    parts += [f'<circle cx="{c}" cy="{c}" r="{r - 0.38:.2f}" fill="none" stroke="url(#limb)" stroke-width=".75"/>',
              f'<path d="{p.ribbon(2.6)}" fill="{ACCENT}" fill-opacity=".28"/>',
              f'<path d="{p.ribbon()}" fill="url(#beam)"/>',
              f'<circle cx="{p.a[0]:.2f}" cy="{p.a[1]:.2f}" r="2.1" fill="{css(mix(CITY, GLOW, 0.1))}"/>',
              f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="6.6" fill="url(#halo)"/>',
              f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="2.9" fill="{CITY}"/>',
              f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="1.6" fill="{css(mix(CITY, GLOW, 0.8))}"/>']
    return '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">\n' + "\n".join(parts) + "\n</svg>\n"


def png(width: int, rows: list[bytes]) -> bytes:
    """An RGB image, written with nothing but zlib."""
    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    raw = b"".join(b"\x00" + row for row in rows)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, len(rows), 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b""))


def ground(p: Picture, pixels: int) -> bytes:
    """The smooth part of the whole icon, a pixel at a time: the ink, light
    scattered off the lit limb, the atmosphere, and the sea and ground under
    the dots, where land shows as a soft glow."""
    share = p.cells()

    def land_under(x: float, y: float) -> float:
        fx, fy = (x - p.cx) / p.pitch - 0.5, (y - p.cy) / p.pitch - 0.5
        i, j = math.floor(fx), math.floor(fy)
        tx, ty = fx - i, fy - j
        at = lambda a, b: share.get((a, b), 0.0)
        return (at(i, j) * (1 - tx) + at(i + 1, j) * tx) * (1 - ty) + (at(i, j + 1) * (1 - tx) + at(i + 1, j + 1) * tx) * ty

    scale = p.size / pixels
    top, bottom = rgb(SKY[0]), rgb(SKY[1])
    limb_light, air_colour = mix(ACCENT2, GLOW, 0.35), mix(ACCENT2, GLOW, 0.2)
    warm, accent2, glint = mix(EMBER, CITY, 0.5), rgb(ACCENT2), rgb(SEA_GLINT)
    air = 0.07 * p.radius
    rows = []
    for py in range(pixels):
        row = bytearray()
        for px in range(pixels):
            fx, fy = (px + 0.5) * scale, (py + 0.5) * scale
            c = mix(top, bottom, fy / p.size)
            x, y = p.disc(fx, fy)
            r = math.hypot(x, y) * p.radius
            if r > p.radius - 2 * scale:
                sun = sunward(x, y)
                a = 0.12 * math.exp(-(r - p.radius) / (0.3 * p.radius)) * (0.2 + 0.8 * sun ** 1.5)
                c = mix(c, accent2, a)
                inside = clamp((p.radius - r) / scale + 0.5)
                if inside < 1:
                    f = clamp(1 - (r - p.radius) / air)
                    c = mix(c, air_colour, f ** 2.4 * (0.04 + 0.6 * sun ** 1.6) * (1 - inside))
            else:
                inside = 1.0
            if inside > 0:
                s = min(1.0, math.hypot(x, y))
                ux, uy = (x / s * 0.999, y / s * 0.999) if s > 0.999 else (x, y)
                z, nl, sun = depth(ux, uy), facing(ux, uy), sunward(x, y)
                level, ground_share = daylight(ux, uy), land_under(fx, fy)
                g = mix(mix(SEA_NIGHT, SEA_DAY, level), mix(GROUND_DARK, GROUND_DAY, level), ground_share)
                spec = max(0.0, ux * HALFWAY[0] + uy * HALFWAY[1] + z * HALFWAY[2]) ** 30
                g = mix(g, glint, spec * 0.45 * (1 - ground_share))
                limb = math.exp(-(p.radius - r) / p.radius / 0.016) * (0.3 + 0.85 * sun ** 1.3) * (0.35 + 0.65 * smoothstep(-0.15, 0.35, nl))
                g = mix(g, limb_light, limb)
                w = warmth(ux, uy) * 0.2
                g = tuple(min(1.0, v + h * w) for v, h in zip(g, warm))
                c = mix(c, g, inside)
            row += bytes(round(clamp(v) * 255) for v in c)
        rows.append(bytes(row))
    return png(pixels, rows)


def touch_icon(pixels: int) -> str:
    """The whole mark, for a square the system rounds itself."""
    p = Picture(1024, 368, (512, 520), pitch=10, beam=18)
    image = base64.b64encode(ground(p, pixels)).decode()
    # A few faint stars, the same ones every time.
    seed, mask = 0x9E3779B97F4A7C15, (1 << 64) - 1

    def random() -> float:
        nonlocal seed
        seed ^= (seed << 13) & mask
        seed ^= seed >> 7
        seed ^= (seed << 17) & mask
        return (seed % 1_000_000) / 1_000_000
    stars = []
    for _ in range(28):
        x, y, m = random() * 1024, random() * 1024, random()
        if math.hypot(x - p.cx, y - p.cy) < p.radius * 1.07 + 24:
            continue
        stars.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{1.5 * (1 + m):.2f}" '
                     f'fill="{css(mix(INK, mix(ACCENT2, GLOW, 0.4), 0.22 + 0.3 * m * m))}"/>')
    core = mix(GLOW, ACCENT, 0.12)
    halo = css(mix(CITY, EMBER, 0.3))
    whole = 'filterUnits="userSpaceOnUse" x="0" y="0" width="1024" height="1024"'
    defs = (f'<defs><linearGradient id="along" gradientUnits="userSpaceOnUse" x1="{p.a[0]:.2f}" y1="{p.a[1]:.2f}" x2="{p.b[0]:.2f}" y2="{p.b[1]:.2f}">'
            f'<stop offset="0" stop-color="{ACCENT}" stop-opacity=".25"/><stop offset="1" stop-color="{ACCENT}"/></linearGradient>'
            f'<linearGradient id="beam" gradientUnits="userSpaceOnUse" x1="{p.a[0]:.2f}" y1="{p.a[1]:.2f}" x2="{p.b[0]:.2f}" y2="{p.b[1]:.2f}">'
            f'<stop offset="0" stop-color="{css(mix(ACCENT, core, 0.45))}" stop-opacity=".85"/><stop offset="1" stop-color="{css(core)}"/></linearGradient>'
            f'<filter id="bloom" {whole}><feGaussianBlur stdDeviation="16"/></filter>'
            f'<filter id="halo" {whole}><feGaussianBlur stdDeviation="22"/></filter></defs>')
    parts = [defs, f'<image href="data:image/png;base64,{image}" width="1024" height="1024"/>', *stars,
             *p.dots(size=(0.24, 0.24), clearing=22, keepout=8),
             f'<path d="{p.ribbon(2.8)}" fill="url(#along)" filter="url(#bloom)"/>',
             f'<path d="{p.ribbon()}" fill="url(#beam)"/>',
             f'<circle cx="{p.a[0]:.2f}" cy="{p.a[1]:.2f}" r="12" fill="{css(mix(CITY, GLOW, 0.1))}"/>',
             f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="52" fill="{halo}" fill-opacity=".9" filter="url(#halo)"/>',
             f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="17" fill="{CITY}"/>',
             f'<circle cx="{p.b[0]:.2f}" cy="{p.b[1]:.2f}" r="9.35" fill="{css(mix(CITY, GLOW, 0.8))}"/>']
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="{pixels // 2}" height="{pixels // 2}">'
            + "".join(parts) + "</svg>")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--browser", help="Chromium to paint with (default: $CHROME, or one on PATH)")
    args = parser.parse_args()
    (SITE / "favicon.svg").write_text(favicon(), encoding="utf-8")
    print("wrote site/public/favicon.svg")
    # The ground is computed at twice the icon's size and the page draws it
    # down, so the globe's edge and its shading are smooth.
    page = (f'<!doctype html><html><body style="margin:0;background:{INK}">{touch_icon(360)}</body></html>')
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
