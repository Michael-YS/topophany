#!/usr/bin/env python3
"""Write the topophany.com pages in every language the site has words for.

site/src holds each page once, with {{key}} where words go, and
site/src/locales/<code>.json the words of one language. The language named
"default" in site/src/site.json is served at /, every other at /<code>/, and
each page links to all of them. The pages are written into site/public, which
is published as is, so they are committed; --check only says which are stale.
The sitemap and robots.txt are written with them, from the same languages.

A key ending in .html holds markup and goes in as written; any other is text
and is escaped. Words may name {{site.*}} values (versions, the origin) so a
fact is kept in one place. js.* keys and each language's name reach the
page's scripts as JSON.

The styles and scripts in site/src/css and site/src/js are published under
names that carry a digest of what they hold (css/site.<digest>.css), and the
data the scripts fetch is named with its digest as a query, so all of them
may be cached for good: a page names the files it was written with, and a
browser never pairs a new page with an old script. A script's imports are
renamed in it before it is digested, so a change to one renames every script
that imports it. Published files no page names any more are removed.
"""
from __future__ import annotations

import argparse
import hashlib
import itertools
import json
import math
from pathlib import Path
import re
import sys
import unicodedata

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "site" / "src"
OUT = ROOT / "site" / "public"
PAGES = ("index.html", "404.html", "demo/index.html", "app/index.html")
STATIC = ("css", "js")
PLACEHOLDER = re.compile(r"\{\{([a-z0-9][a-z0-9._-]*)\}\}")
IMPORT = re.compile(r"""(\b(?:from|import)\s*)(['"])\./([\w.-]+\.js)\2""")
# The vantage panel shows a language's preview as it places these nodes: the
# MacBook at home, the transit host it cannot reach and the New York server
# that can. It is 520 units wide, the land is drawn between rows 8 and 166,
# and the places keep to rows 34 to 124.
PREVIEW = ROOT / "internal" / "preview"
VANTAGE = {"home": "mac", "target": "transit", "away": "app"}
VIEW = 520
LAND = (8, 166)
ROWS = (34.0, 124.0)


class SiteError(Exception):
    pass


def load(src: Path = SRC) -> tuple[dict, dict[str, dict[str, str]]]:
    site = json.loads((src / "site.json").read_text(encoding="utf-8"))
    catalogs = {path.stem: json.loads(path.read_text(encoding="utf-8"))
                for path in sorted((src / "locales").glob("*.json"))}
    if site.get("default") not in catalogs:
        raise SiteError(f"site.json names default language {site.get('default')!r}, which has no catalog")
    base = set(catalogs[site["default"]])
    for code, words in catalogs.items():
        missing, extra = sorted(base - set(words)), sorted(set(words) - base)
        if missing or extra:
            raise SiteError(f"locales/{code}.json: missing {missing}, unknown {extra}")
    return site, catalogs


def languages(site: dict, catalogs: dict) -> list[str]:
    """The default language first, then the others by code."""
    return sorted(catalogs, key=lambda code: (code != site["default"], code))


def path_of(site: dict, code: str) -> str:
    return "/" if code == site["default"] else f"/{code}/"


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()[:12]


def assets(src: Path = SRC, out: Path = OUT) -> dict[str, tuple[Path, str]]:
    """Every style and script, by the path a page would name it with
    (/js/site.js), as the path it is published at and what it holds."""
    published = {}
    for kind in STATIC:
        sources = {path.name: path.read_text(encoding="utf-8") for path in sorted((src / kind).glob(f"*.{kind}"))}
        names: dict[str, str] = {}

        def publish(name: str, importers: tuple[str, ...] = ()) -> str:
            if name in importers:
                raise SiteError(f"{kind}/{name} imports itself through {' → '.join(importers)}")
            if name not in sources:
                raise SiteError(f"{kind}/{importers[-1]} imports {name}, which site/src/{kind} does not have")
            if name not in names:
                text = sources[name]
                if kind == "js":
                    text = IMPORT.sub(lambda m: f"{m[1]}{m[2]}./{publish(m[3], importers + (name,))}{m[2]}", text)
                stem, _, suffix = name.rpartition(".")
                names[name] = f"{stem}.{digest(text.encode())}.{suffix}"
                published[f"/{kind}/{name}"] = (out / kind / names[name], text)
            return names[name]

        for name in sources:
            publish(name)
    license_file = src / "js/wasm_exec.LICENSE"
    if license_file.is_file():
        published["/js/wasm_exec.LICENSE"] = (out / "js/wasm_exec.LICENSE", license_file.read_text())
    return published


def data_url(out: Path, name: str) -> str | None:
    """A data file's path with its digest as a query, or None while it is
    not recorded."""
    path = out / "data" / name
    return f"/data/{name}?v={digest(path.read_bytes())}" if path.is_file() else None


def tour_size(out: Path, code: str) -> dict[str, str]:
    """The recorded tour's terminal size, "200×50", for the replay's title
    bar, or nothing while it is not recorded."""
    path = out / "data" / f"tour.{code}.json"
    if not path.is_file():
        return {}
    cols, rows = json.loads(path.read_text(encoding="utf-8"))["size"]
    return {"page.tour.size": f"{cols}×{rows}"}


def page_values(site: dict, catalogs: dict, code: str, urls: dict[str, str] | None = None,
                out: Path = OUT, suffix: str = "") -> dict[str, str]:
    words = catalogs[code]
    here = path_of(site, code)
    urls = urls or {}
    links, items, offers, locales = [], [], [], []
    for other in languages(site, catalogs):
        theirs, where = catalogs[other], path_of(site, other) + suffix
        tag = escape(theirs["lang.tag"])
        links.append(f'<link rel="alternate" hreflang="{tag}" href="{escape(site["origin"] + where)}">')
        if other != code and "lang.locale" in theirs:
            locales.append(f'<meta property="og:locale:alternate" content="{escape(theirs["lang.locale"])}">')
        current = ' aria-current="page"' if other == code else ""
        items.append(f'<li><a href="{where}" hreflang="{tag}" lang="{tag}"{current}>{escape(theirs["lang.name"])}</a></li>')
        offers.append({"tag": theirs["lang.tag"], "href": where, "name": theirs["lang.name"],
                       "suggest": theirs["lang.suggest"], "dismiss": theirs["lang.dismiss"]})
    links.append(f'<link rel="alternate" hreflang="x-default" href="{escape(site["origin"] + "/" + suffix)}">')
    strings = {key[3:]: expand(value, site, {}) for key, value in words.items() if key.startswith("js.")}
    strings["languages"] = offers
    # A data block is not run, but "</" could still end the element early.
    data = json.dumps(strings, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    files = {"page.style": urls.get("/css/site.css"), "page.script": urls.get("/js/site.js"),
             "page.demo-style": urls.get("/css/demo.css"), "page.demo-script": urls.get("/js/demo.js"),
             "page.demo-worker": urls.get("/js/demo-worker.js"), "page.wasm": data_url(out, "demo.wasm"),
             "page.collector-worker": urls.get("/js/collector-worker.js"),
             "page.viewer-wasm": data_url(out, "viewer.wasm"),
             "page.land": data_url(out, "land.bin"), "page.tour": data_url(out, f"tour.{code}.json")}
    return {
        "page.code": code,
        "page.path": here,
        "page.origin": site["origin"],
        "page.url": site["origin"] + here + suffix,
        "page.demo": here + "demo/",
        "page.app": here + "app/",
        "page.alternates.html": "\n".join(links),
        "page.languages.html": "".join(items),
        "page.strings.html": f'<script type="application/json" id="strings">{data}</script>',
        "page.locales.html": "\n".join(locales),
        "page.jsonld.html": jsonld(site, catalogs, code),
        **{key: url for key, url in files.items() if url},
        **tour_size(out, code),
        **vantage(code, words),
    }


def vantage(code: str, words: dict[str, str]) -> dict[str, str]:
    """The vantage panel drawn where the language's preview puts its places:
    an equirectangular map fitted to them, arcs that cross no label, and
    every label inside the panel."""
    path = PREVIEW / f"topology.{code}.json"
    if not path.is_file() or "vantage.home" not in words:
        return {}
    nodes = {n["id"]: n.get("location") for n in json.loads(path.read_text(encoding="utf-8"))["nodes"]}
    at = {}
    for role, node in VANTAGE.items():
        if not nodes.get(node):
            raise SiteError(f"{path.relative_to(ROOT)}: node {node!r} has no location for the vantage panel")
        at[role] = (nodes[node]["lat"], nodes[node]["lon"])
    # The places as close together as the antimeridian allows.
    west = min((lon for _, lon in at.values()), key=lambda w: max((lon - w) % 360 for _, lon in at.values()))
    at = {role: (lat, west + (lon - west) % 360) for role, (lat, lon) in at.items()}
    left, right = 40.0, VIEW - 24.0
    for _ in range(8):
        values, (lo, hi), overlap = min((vantage_layout(at, left, right, words, choice) for choice in CHOICES),
                                        key=lambda laid: laid[2])
        if lo >= 4 and hi <= VIEW - 4:
            break
        # A unit more than it spills, since moving the places also scales them.
        left, right = left + max(0.0, 5 - lo) * (lo < 4), right - max(0.0, hi - (VIEW - 5)) * (hi > VIEW - 4)
    if overlap or lo < 4 or hi > VIEW - 4:
        raise SiteError(f"locales/{code}.json: the vantage panel cannot be drawn without overlaps; shorten its words")
    return values


# How the panel may be drawn, the first choice that overlaps nothing
# winning: whether each arc bows the other way than up (or, for an arc
# running north to south, away from the third place), and where each
# place's label goes, as an anchor and an offset from the place.
HOME_LABELS = [("start", 12, 18), ("end", -12, 18)]
TARGET_LABELS = [("middle", 0, 23), ("end", -6, 23), ("start", 6, 23)]
AWAY_LABELS = [("end", 17.4, 22), ("middle", 0, 23), ("end", 17.4, -24), ("middle", 0, -26)]
CHOICES = list(itertools.product((False, True), (False, True), HOME_LABELS, TARGET_LABELS, AWAY_LABELS))


def text_width(text: str, size: float) -> float:
    return sum(size if unicodedata.east_asian_width(c) in "WF" else size * 0.6 for c in text)


def vantage_layout(at: dict, left: float, right: float, words: dict[str, str],
                   choice: tuple) -> tuple[dict[str, str], tuple[float, float], int]:
    """The panel's values with the places between columns left and right,
    drawn as choice says; how far its labels reach to either side; and how
    much of it overlaps."""
    bad_flip, ok_flip, home_label, target_label, away_label = choice
    lats, lons = [lat for lat, _ in at.values()], [lon for _, lon in at.values()]
    top, bottom = ROWS
    scale = min((right - left) / (max(lons) - min(lons)), (bottom - top) / (max(lats) - min(lats)))
    x0 = (left + right - (max(lons) - min(lons)) * scale) / 2
    y0 = (top + bottom - (max(lats) - min(lats)) * scale) / 2
    pos = {role: (x0 + (lon - min(lons)) * scale, y0 + (max(lats) - lat) * scale) for role, (lat, lon) in at.items()}
    home, target, away = pos["home"], pos["target"], pos["away"]
    boxes = [(x - 7, y - 7, x + 7, y + 7) for x, y in pos.values()]  # what may not be crossed

    def label(x: float, y: float, anchor: str, *lines: tuple[str, float]) -> list[tuple[float, float, float, float]]:
        """The boxes of lines of text, 14 apart, from baseline y."""
        out = []
        for i, (text, size) in enumerate(lines):
            width = text_width(text, size)
            start = {"start": x, "middle": x - width / 2, "end": x - width}[anchor]
            base = y + 14 * i
            out.append((start - 2, base - 0.75 * size - 1, start + width + 2, base + 0.2 * size + 1))
        return out

    def bow(a, b, third, flip: bool):
        """The control point, middle and outer normal of an arc from a to b."""
        dx, dy = b[0] - a[0], b[1] - a[1]
        length = math.hypot(dx, dy)
        nx, ny = dy / length, -dx / length
        if abs(dy) < abs(dx):
            turn = ny > 0
        else:
            turn = (third[0] - (a[0] + b[0]) / 2) * nx > 0
        if turn != flip:
            nx, ny = -nx, -ny
        sag = 0.13 * length
        mx, my = (a[0] + b[0]) / 2, (a[1] + b[1]) / 2
        return (mx + 2 * sag * nx, my + 2 * sag * ny), (mx + sag * nx, my + sag * ny), (nx, ny)

    def evidence(middle, normal, gap: float, text: str):
        """Where an arc's evidence goes: beyond its middle, on its outer side."""
        (x, y), (nx, ny) = middle, normal
        if abs(ny) >= abs(nx):
            at, anchor = (x, y - gap if ny < 0 else y + gap + 8), "middle"
        else:
            at, anchor = (x + (gap if nx > 0 else -gap), y + 4), "start" if nx > 0 else "end"
        return at, anchor, label(*at, anchor, (text, 11.5))

    bad_control, bad_middle, bad_normal = bow(home, target, away, bad_flip)
    ok_control, ok_middle, ok_normal = bow(away, target, home, ok_flip)
    # The arrow ends at the target's ring, not under its dot.
    ux, uy = ok_control[0] - target[0], ok_control[1] - target[1]
    end = (target[0] + 5.5 * ux / math.hypot(ux, uy), target[1] + 5.5 * uy / math.hypot(ux, uy))
    bad_at, bad_anchor, bad_box = evidence(bad_middle, bad_normal, 15, words["vantage.bad"])
    ok_at, ok_anchor, ok_box = evidence(ok_middle, ok_normal, 13.5, words["vantage.ok"])
    place, name = lambda key: (words[key], 12.5), lambda key: (words[key], 11)

    def put(where, anchor: str, dx: float, dy: float):
        return (where[0] + dx, where[1] + dy), anchor

    home_at, home_anchor = put(home, *home_label)
    target_at, target_anchor = put(target, *target_label)
    away_at, away_anchor = put(away, *away_label)
    labels = [bad_box, ok_box,
              label(*home_at, home_anchor, place("vantage.home"), name("vantage.home-worker")),
              label(*target_at, target_anchor, place("vantage.target"), name("vantage.target-name")),
              label(*away_at, away_anchor, place("vantage.away"), name("vantage.away-worker"))]
    boxes += [box for lines in labels for box in lines]

    def inside(x: float, y: float, box) -> bool:
        return box[0] <= x <= box[2] and box[1] <= y <= box[3]

    def curve(a, control, b):
        steps = 60
        return [((1 - t) ** 2 * a[0] + 2 * (1 - t) * t * control[0] + t * t * b[0],
                 (1 - t) ** 2 * a[1] + 2 * (1 - t) * t * control[1] + t * t * b[1])
                for t in (i / steps for i in range(4, steps - 3))]

    def meet(a, b) -> bool:
        return a[0] < b[2] and b[0] < a[2] and a[1] < b[3] and b[1] < a[3]

    overlap = 0
    for point in curve(home, bad_control, target) + curve(away, ok_control, end):
        overlap += sum(inside(*point, box) for lines in labels for box in lines)
        overlap += 10 * (not 2 <= point[1] <= LAND[1])
    for i, lines in enumerate(labels):
        for a in lines:
            overlap += 10 * (a[1] < 0 or a[3] > LAND[1] + 4)
            overlap += sum(10 * meet(a, city) for city in boxes[:3])
            overlap += sum(10 * meet(a, b) for other in labels[i + 1:] for b in other)
    point = lambda p: f"{p[0]:.1f} {p[1]:.1f}"
    values = {
        "page.vantage.lon": f"{min(lons) - x0 / scale:.2f}",
        "page.vantage.lat": f"{max(lats) + (y0 - LAND[0]) / scale:.2f}",
        "page.vantage.scale": f"{scale:.3f}",
        "page.vantage.bad": f"M{point(home)}Q{point(bad_control)} {point(target)}",
        "page.vantage.ok": f"M{point(away)}Q{point(ok_control)} {point(end)}",
        "page.vantage.x": point(bad_middle),
        "page.vantage.bad-at": point(bad_at), "page.vantage.bad-anchor": bad_anchor,
        "page.vantage.ok-at": point(ok_at), "page.vantage.ok-anchor": ok_anchor,
        "page.vantage.home": point(home), "page.vantage.target": point(target), "page.vantage.away": point(away),
        "page.vantage.home-label": point(home_at), "page.vantage.home-anchor": home_anchor,
        "page.vantage.target-label": point(target_at), "page.vantage.target-anchor": target_anchor,
        "page.vantage.away-label": point(away_at), "page.vantage.away-anchor": away_anchor,
    }
    return values, (min(b[0] for b in boxes), max(b[2] for b in boxes)), overlap


def jsonld(site: dict, catalogs: dict, code: str) -> str:
    """What the page is about, for search engines: the site, and the
    software it is the homepage of, in the page's language."""
    words, origin = catalogs[code], site["origin"]
    repo = "https://github.com/WhitenWhiten/topophany"
    software = {
        "@type": "SoftwareApplication", "@id": f"{origin}/#software", "name": "Topophany",
        "url": origin + path_of(site, code), "inLanguage": words["lang.tag"],
        "applicationCategory": "DeveloperApplication", "applicationSubCategory": "Network monitoring",
        "operatingSystem": "Linux, macOS, Windows",
        "license": f"{repo}/blob/main/LICENSE", "downloadUrl": f"{repo}/releases/latest",
        "image": f"{origin}/og.png", "sameAs": [repo],
        "offers": {"@type": "Offer", "price": "0", "priceCurrency": "USD"},
    }
    if "meta.description" in words:
        software["description"] = expand(words["meta.description"], site, {})
    if "version" in site:
        software["softwareVersion"] = site["version"].lstrip("v")
    data = {
        "@context": "https://schema.org",
        "@graph": [
            {"@type": "WebSite", "@id": f"{origin}/#website", "url": f"{origin}/", "name": "Topophany",
             "inLanguage": [catalogs[c]["lang.tag"] for c in languages(site, catalogs)]},
            software,
        ],
    }
    text = json.dumps(data, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    return f'<script type="application/ld+json">{text}</script>'


def expand(value: str, site: dict, page: dict[str, str]) -> str:
    """Fills the {{site.*}} and {{page.*}} values that words may name."""
    def fill(match: re.Match) -> str:
        key = match.group(1)
        if key.startswith("site.") and key[5:] in site:
            return str(site[key[5:]])
        if key in page and not key.endswith(".html"):
            return page[key]
        raise SiteError(f"words may name only site and page values, not {key!r}")
    return PLACEHOLDER.sub(fill, value)


def render(template: str, name: str, site: dict, words: dict[str, str], page: dict[str, str], used: set[str]) -> str:
    def fill(match: re.Match) -> str:
        key = match.group(1)
        before = template[:match.start()]
        in_tag = before.rfind("<") > before.rfind(">")
        if key.startswith("site."):
            value = expand(match.group(0), site, page)
        elif key in page:
            value = page[key]
        elif key in words:
            used.add(key)
            value = expand(words[key], site, page)
        elif key.startswith("page."):
            raise SiteError(f"{name}: nothing for {{{{{key}}}}}; is site/public/data recorded (make site)?")
        else:
            raise SiteError(f"{name}: no words for {{{{{key}}}}}")
        if key.endswith(".html"):
            if in_tag:
                raise SiteError(f"{name}: markup {{{{{key}}}}} inside a tag")
            return value
        return escape(value)
    return PLACEHOLDER.sub(fill, template)


def escape(text: str) -> str:
    """Text for an element or a double-quoted attribute, as written otherwise."""
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;")


def sitemap(site: dict, catalogs: dict) -> str:
    """Every language's page, each naming all of them as its alternates."""
    urls = ""
    for suffix in ("", "demo/", "app/"):
        alternates = [(catalogs[c]["lang.tag"], site["origin"] + path_of(site, c) + suffix) for c in languages(site, catalogs)]
        alternates.append(("x-default", site["origin"] + "/" + suffix))
        links = "".join(f'\n    <xhtml:link rel="alternate" hreflang="{escape(tag)}" href="{escape(href)}"/>'
                        for tag, href in alternates)
        urls += "".join(f"\n  <url>\n    <loc>{escape(href)}</loc>{links}\n  </url>" for tag, href in alternates[:-1])
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">'
            f"{urls}\n</urlset>\n")


def robots(site: dict) -> str:
    """Everything may be crawled, the scripts' data included, since a page
    is judged as it renders."""
    return f"User-agent: *\nAllow: /\n\nSitemap: {site['origin']}/sitemap.xml\n"


def build(src: Path = SRC, out: Path = OUT) -> dict[Path, str]:
    """Every page in every language, by the path it is published from, with
    the styles and scripts they name, the sitemap and robots.txt."""
    site, catalogs = load(src)
    templates = {name: (src / name).read_text(encoding="utf-8") for name in PAGES}
    published = assets(src, out)
    urls = {path: "/" + file.relative_to(out).as_posix() for path, (file, _) in published.items()}
    pages, used = {file: text for file, text in published.values()}, set()
    for code in languages(site, catalogs):
        for name, template in templates.items():
            suffix = name.removesuffix("index.html") if name in ("demo/index.html", "app/index.html") else ""
            values = page_values(site, catalogs, code, urls, out, suffix)
            pages[out / path_of(site, code).strip("/") / name] = render(template, name, site, catalogs[code], values, used)
    unused = sorted(key for key in catalogs[site["default"]]
                    if key not in used and not key.startswith(("js.", "lang.")))
    if unused:
        raise SiteError(f"words no page uses: {unused}")
    pages[out / "sitemap.xml"] = sitemap(site, catalogs)
    pages[out / "robots.txt"] = robots(site)
    return pages


def strays(pages: dict[Path, str], out: Path = OUT) -> list[Path]:
    """Styles and scripts published earlier that no page names now."""
    return sorted(path for kind in STATIC for path in (out / kind).glob("*") if path not in pages)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--check", action="store_true", help="write nothing; fail if a page is stale")
    args = parser.parse_args()
    try:
        pages = build()
    except SiteError as exc:
        print(f"build_site: {exc}", file=sys.stderr)
        return 1
    stale = [path for path, text in pages.items()
             if not path.is_file() or path.read_text(encoding="utf-8") != text]
    unused = strays(pages)
    for path in stale:
        if args.check:
            print(f"stale: {path.relative_to(ROOT)}", file=sys.stderr)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(pages[path], encoding="utf-8")
            print(f"wrote {path.relative_to(ROOT)}")
    for path in unused:
        if args.check:
            print(f"unused: {path.relative_to(ROOT)}", file=sys.stderr)
        else:
            path.unlink()
            print(f"removed {path.relative_to(ROOT)}")
    if args.check and (stale or unused):
        print("build_site: run `python3 scripts/build_site.py` (or `make site`)", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
