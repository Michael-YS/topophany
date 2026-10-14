#!/usr/bin/env python3
"""Write the topophany.com pages in every language the site has words for.

site/src holds each page once, with {{key}} where words go, and
site/src/locales/<code>.json the words of one language. The language named
"default" in site/src/site.json is served at /, every other at /<code>/, and
each page links to all of them. The pages are written into site/public, which
is published as is, so they are committed; --check only says which are stale.

A key ending in .html holds markup and goes in as written; any other is text
and is escaped. Words may name {{site.*}} values (versions, the origin) so a
fact is kept in one place. js.* keys and each language's name reach the
page's scripts as JSON.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]
SRC = ROOT / "site" / "src"
OUT = ROOT / "site" / "public"
PAGES = ("index.html", "404.html")
PLACEHOLDER = re.compile(r"\{\{([a-z0-9][a-z0-9._-]*)\}\}")


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


def page_values(site: dict, catalogs: dict, code: str) -> dict[str, str]:
    words = catalogs[code]
    here = path_of(site, code)
    links, items, offers = [], [], []
    for other in languages(site, catalogs):
        theirs, where = catalogs[other], path_of(site, other)
        tag = escape(theirs["lang.tag"])
        links.append(f'<link rel="alternate" hreflang="{tag}" href="{escape(site["origin"] + where)}">')
        current = ' aria-current="page"' if other == code else ""
        items.append(f'<li><a href="{where}" hreflang="{tag}" lang="{tag}"{current}>{escape(theirs["lang.name"])}</a></li>')
        offers.append({"tag": theirs["lang.tag"], "href": where, "name": theirs["lang.name"],
                       "suggest": theirs["lang.suggest"], "dismiss": theirs["lang.dismiss"]})
    links.append(f'<link rel="alternate" hreflang="x-default" href="{escape(site["origin"] + "/")}">')
    strings = {key[3:]: expand(value, site, {}) for key, value in words.items() if key.startswith("js.")}
    strings["languages"] = offers
    # A data block is not run, but "</" could still end the element early.
    data = json.dumps(strings, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    return {
        "page.code": code,
        "page.path": here,
        "page.origin": site["origin"],
        "page.url": site["origin"] + here,
        "page.alternates.html": "\n".join(links),
        "page.languages.html": "".join(items),
        "page.strings.html": f'<script type="application/json" id="strings">{data}</script>',
    }


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


def build(src: Path = SRC, out: Path = OUT) -> dict[Path, str]:
    """Every page in every language, by the path it is published from."""
    site, catalogs = load(src)
    templates = {name: (src / name).read_text(encoding="utf-8") for name in PAGES}
    pages, used = {}, set()
    for code in languages(site, catalogs):
        values = page_values(site, catalogs, code)
        for name, template in templates.items():
            pages[out / path_of(site, code).strip("/") / name] = render(template, name, site, catalogs[code], values, used)
    unused = sorted(key for key in catalogs[site["default"]]
                    if key not in used and not key.startswith(("js.", "lang.")))
    if unused:
        raise SiteError(f"words no page uses: {unused}")
    return pages


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
    for path in stale:
        if args.check:
            print(f"stale: {path.relative_to(ROOT)}", file=sys.stderr)
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(pages[path], encoding="utf-8")
            print(f"wrote {path.relative_to(ROOT)}")
    if args.check and stale:
        print("build_site: run `python3 scripts/build_site.py` (or `make site`)", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
