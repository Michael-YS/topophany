#!/usr/bin/env python3
"""Rasterize Natural Earth 1:110m land polygons (public domain) into the packed
bitmap embedded by internal/tui (720x360 cells, 0.5 degree, row 0 = 90N,
column 0 = 180W, most significant bit first).

Usage: python3 scripts/gen_landmask.py ne_110m_land.geojson internal/tui/land.bin
Source: https://github.com/nvkelso/natural-earth-vector (geojson/ne_110m_land.geojson)
"""
import json, sys

W, H = 720, 360


def rings(geometry):
    if geometry['type'] == 'Polygon':
        yield from geometry['coordinates']
    elif geometry['type'] == 'MultiPolygon':
        for polygon in geometry['coordinates']:
            yield from polygon


def main():
    src, dst = sys.argv[1], sys.argv[2]
    data = json.load(open(src))
    edges = []
    for feature in data['features']:
        for ring in rings(feature['geometry']):
            for (x1, y1), (x2, y2) in zip(ring, ring[1:] + ring[:1]):
                if y1 != y2:
                    edges.append((x1, y1, x2, y2))
    bits = bytearray(W * H // 8)
    for row in range(H):
        lat = 90 - (row + .5) * 180 / H
        xs = []
        for x1, y1, x2, y2 in edges:
            if (y1 <= lat) != (y2 <= lat):
                xs.append(x1 + (lat - y1) * (x2 - x1) / (y2 - y1))
        xs.sort()
        for a, b in zip(xs[0::2], xs[1::2]):
            for col in range(W):
                lon = -180 + (col + .5) * 360 / W
                if a <= lon < b:
                    i = row * W + col
                    bits[i // 8] |= 0x80 >> (i % 8)
    # Antarctica's polygon is closed along the antimeridian/south pole; make the
    # polar cap solid so the globe has no seam.
    for row in range(H - 12, H):
        for col in range(W):
            i = row * W + col
            bits[i // 8] |= 0x80 >> (i % 8)
    open(dst, 'wb').write(bytes(bits))


if __name__ == '__main__':
    main()
