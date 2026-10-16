"""The homepage's pages are written from its words, in every language."""
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

SPEC = importlib.util.spec_from_file_location("build_site", Path(__file__).with_name("build_site.py"))
build_site = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_site)

ROOT = build_site.ROOT
SITE = ROOT / "site"


@unittest.skipUnless(SITE.is_dir(), "the site is not part of a source export")
class SiteTests(unittest.TestCase):
    def test_browser_binaries_are_generated_outside_git(self):
        outputs = ["site/public/data/demo.wasm", "site/public/data/viewer.wasm",
                   "site/public/data/demo.build.json"]
        tracked = subprocess.check_output(["git", "ls-files", "--", *outputs], cwd=ROOT, text=True)
        self.assertEqual(tracked, "", "browser binaries and build cache must not enter Git history")
        ignored = subprocess.check_output(["git", "check-ignore", "--", *outputs], cwd=ROOT, text=True)
        self.assertEqual(set(ignored.splitlines()), set(outputs))

    def test_pages_are_written_from_the_current_words(self):
        for path, text in build_site.build().items():
            self.assertTrue(path.is_file(), f"{path.relative_to(ROOT)} is missing; run make site")
            self.assertEqual(path.read_text(encoding="utf-8"), text,
                             f"{path.relative_to(ROOT)} is stale; run make site")

    def test_nothing_unused_is_published(self):
        self.assertEqual(build_site.strays(build_site.build()), [], "run make site")

    def test_pages_name_files_by_what_they_hold(self):
        pages = build_site.build()
        for path, text in pages.items():
            if path.suffix != ".html":
                continue
            names = re.findall(r'(?:href|src|data-tour)="(/(?:css|js|data)/[^"]+)"', text)
            self.assertTrue(names, f"{path.relative_to(ROOT)} names no styles, scripts or data")
            for name in names:
                file, _, query = name.partition("?")
                published = build_site.OUT / file.lstrip("/")
                if file.startswith("/data/"):
                    self.assertEqual(query, "v=" + build_site.digest(published.read_bytes()), name)
                else:
                    self.assertIn(published, pages, name)
                    self.assertIn("." + build_site.digest(pages[published].encode()) + ".", published.name)

    def test_a_script_is_renamed_when_what_it_imports_changes(self):
        with tempfile.TemporaryDirectory() as temp:
            src = Path(temp)
            (src / "js").mkdir()
            (src / "js/a.js").write_text("import { b } from './b.js';\nb();\n")
            (src / "js/b.js").write_text("export const b = () => 1;\n")
            before = build_site.assets(src, src / "out")
            self.assertIn(f"from './{before['/js/b.js'][0].name}'", before["/js/a.js"][1])
            (src / "js/b.js").write_text("export const b = () => 2;\n")
            after = build_site.assets(src, src / "out")
            self.assertNotEqual(before["/js/a.js"][0], after["/js/a.js"][0])
            (src / "js/b.js").write_text("import './a.js';\n")
            with self.assertRaisesRegex(build_site.SiteError, "imports itself"):
                build_site.assets(src, src / "out")

    def test_the_site_and_the_preview_speak_the_same_languages(self):
        site, catalogs = build_site.load()
        preview = {path.stem for path in (ROOT / "internal/preview/locales").glob("*.json")}
        self.assertEqual(set(catalogs), preview)
        for code in catalogs:
            tour = SITE / "public/data" / f"tour.{code}.json"
            self.assertTrue(tour.is_file(), f"no recorded tour in {code}; run make site")
            self.assertEqual(len(json.loads(tour.read_text(encoding="utf-8"))["names"]), 9)

    def test_every_language_page_links_every_other(self):
        site, catalogs = build_site.load()
        pages = build_site.build()
        for code in catalogs:
            page = pages[build_site.OUT / build_site.path_of(site, code).strip("/") / "index.html"]
            self.assertIn(f'<html lang="{catalogs[code]["lang.tag"]}">', page)
            for other in catalogs:
                href = site["origin"] + build_site.path_of(site, other)
                self.assertIn(f'hreflang="{catalogs[other]["lang.tag"]}" href="{href}"', page)
            self.assertNotRegex(page, r"\{\{|\}\}")

    def test_search_engines_find_every_language(self):
        site, catalogs = build_site.load()
        pages = build_site.build()
        sitemap = ET.fromstring(pages[build_site.OUT / "sitemap.xml"])
        ns = {"s": "http://www.sitemaps.org/schemas/sitemap/0.9", "x": "http://www.w3.org/1999/xhtml"}
        hrefs = {code: site["origin"] + build_site.path_of(site, code) for code in catalogs}
        self.assertEqual([u.findtext("s:loc", namespaces=ns) for u in sitemap.findall("s:url", ns)],
                         [hrefs[code] + suffix for suffix in ("", "demo/", "app/")
                          for code in build_site.languages(site, catalogs)])
        for url in sitemap.findall("s:url", ns):
            location = url.findtext("s:loc", namespaces=ns)
            suffix = next((part for part in ("demo/", "app/") if location.endswith("/" + part)), "")
            alternates = {a.get("hreflang"): a.get("href") for a in url.findall("x:link", ns)}
            self.assertEqual(alternates, {**{catalogs[c]["lang.tag"]: h + suffix for c, h in hrefs.items()},
                                          "x-default": site["origin"] + "/" + suffix})
        self.assertIn(f"Sitemap: {site['origin']}/sitemap.xml", pages[build_site.OUT / "robots.txt"])
        for code in catalogs:
            page = pages[build_site.OUT / build_site.path_of(site, code).strip("/") / "index.html"]
            data = json.loads(re.search(r'<script type="application/ld\+json">(.*?)</script>', page).group(1))
            software = next(n for n in data["@graph"] if n["@type"] == "SoftwareApplication")
            self.assertEqual((software["url"], software["inLanguage"]), (hrefs[code], catalogs[code]["lang.tag"]))
            others = {catalogs[c]["lang.locale"] for c in catalogs if c != code}
            self.assertEqual(set(re.findall(r'og:locale:alternate" content="([^"]+)"', page)), others)

    def test_scripts_ask_only_for_words_the_pages_carry(self):
        site, catalogs = build_site.load()
        words = {key[3:] for key in catalogs[site["default"]] if key.startswith("js.")}
        source = "\n".join((SITE / f"src/js/{name}.js").read_text(encoding="utf-8") for name in ("site", "demo"))
        asked = set(re.findall(r"\bt\(\s*'([\w.-]+)'", source))
        asked |= {f"place.{place}" for place in re.findall(r"\{ id: '([\w-]+)'", source)}
        self.assertTrue(asked)
        self.assertLessEqual(asked, words)

    def test_browser_demo_is_current_and_local(self):
        checked = subprocess.run([sys.executable, str(ROOT / "scripts/build_demo.py"), "--check"],
                                 cwd=ROOT, text=True, capture_output=True)
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)
        self.assertEqual((SITE / "public/data/demo.wasm").read_bytes()[:4], b"\x00asm")
        site, catalogs = build_site.load()
        pages = build_site.build()
        for code in catalogs:
            where = build_site.path_of(site, code)
            page = pages[build_site.OUT / where.strip("/") / "demo/index.html"]
            self.assertIn(f'data-language="{code}"', page)
            self.assertIn('data-wasm="/data/demo.wasm?v=', page)
            self.assertIn('data-worker="/js/demo-worker.', page)
            self.assertIn(f'href="{site["origin"] + where}demo/"', page)
            for other in catalogs:
                self.assertIn(f'href="{site["origin"] + build_site.path_of(site, other)}demo/"', page)
            body = page.split("<body", 1)[1]
            self.assertNotRegex(body, r"<(header|nav|button|select|details)\b")
            self.assertIn('id="demo-canvas"', body)
            home = pages[build_site.OUT / where.strip("/") / "index.html"]
            self.assertIn(f'href="{where}demo/"', home)
        policy = (SITE / "public/_headers").read_text()
        self.assertIn("'wasm-unsafe-eval'", policy)
        self.assertNotIn("'unsafe-eval'", policy)

    def test_collector_client_has_separate_runtime_and_connection_form(self):
        site, catalogs = build_site.load()
        pages = build_site.build()
        self.assertEqual((SITE / "public/data/viewer.wasm").read_bytes()[:4], b"\x00asm")
        for code in catalogs:
            where = build_site.path_of(site, code)
            page = pages[build_site.OUT / where.strip("/") / "app/index.html"]
            self.assertIn('data-mode="live"', page)
            self.assertIn('data-wasm="/data/viewer.wasm?v=', page)
            self.assertIn('data-worker="/js/collector-worker.', page)
            self.assertIn('id="collector-connect"', page)
            self.assertIn('id="collector-token"', page)
            self.assertIn('type="password"', page)
            self.assertIn('minlength="32" maxlength="512"', page)
            for other in catalogs:
                self.assertIn(f'href="{site["origin"] + build_site.path_of(site, other)}app/"', page)
        policy = (SITE / "public/_headers").read_text()
        override = policy.split("/js/collector-worker.*.js", 1)[1]
        self.assertIn("! Content-Security-Policy", override)
        self.assertIn("connect-src 'self' https:", override)

    def test_words_are_escaped_unless_they_are_markup(self):
        with tempfile.TemporaryDirectory() as temp:
            src = Path(temp)
            (src / "locales").mkdir()
            (src / "site.json").write_text(json.dumps({"origin": "https://example.invalid", "default": "en"}))
            words = {"lang.tag": "en", "lang.name": "English", "lang.suggest": "s", "lang.dismiss": "d",
                     "a": 'x < "y" & z', "b.html": "<em>b</em>"}
            (src / "locales/en.json").write_text(json.dumps(words))
            for name in build_site.PAGES:
                (src / name).parent.mkdir(parents=True, exist_ok=True)
                (src / name).write_text('<p title="{{a}}">{{a}} {{b.html}}</p>')
            page = build_site.build(src, src / "out")[src / "out" / "index.html"]
            self.assertEqual(page, '<p title="x &lt; &quot;y&quot; &amp; z">x &lt; &quot;y&quot; &amp; z <em>b</em></p>')
            for name in build_site.PAGES:
                (src / name).write_text('<p title="{{b.html}}"></p>')
            with self.assertRaisesRegex(build_site.SiteError, "inside a tag"):
                build_site.build(src, src / "out")
            for name in build_site.PAGES:
                (src / name).write_text("{{nothing}}")
            with self.assertRaisesRegex(build_site.SiteError, "no words"):
                build_site.build(src, src / "out")


if __name__ == "__main__":
    unittest.main()
