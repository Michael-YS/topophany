"""The homepage's pages are written from its words, in every language."""
import importlib.util
import json
from pathlib import Path
import re
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("build_site", Path(__file__).with_name("build_site.py"))
build_site = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(build_site)

ROOT = build_site.ROOT
SITE = ROOT / "site"


@unittest.skipUnless(SITE.is_dir(), "the site is not part of a source export")
class SiteTests(unittest.TestCase):
    def test_pages_are_written_from_the_current_words(self):
        for path, text in build_site.build().items():
            self.assertTrue(path.is_file(), f"{path.relative_to(ROOT)} is missing; run make site")
            self.assertEqual(path.read_text(encoding="utf-8"), text,
                             f"{path.relative_to(ROOT)} is stale; run make site")

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

    def test_scripts_ask_only_for_words_the_pages_carry(self):
        site, catalogs = build_site.load()
        words = {key[3:] for key in catalogs[site["default"]] if key.startswith("js.")}
        source = (SITE / "public/js/site.js").read_text(encoding="utf-8")
        asked = set(re.findall(r"\bt\(\s*'([\w.-]+)'", source))
        asked |= {f"place.{place}" for place in re.findall(r"\{ id: '([\w-]+)'", source)}
        self.assertTrue(asked)
        self.assertLessEqual(asked, words)

    def test_words_are_escaped_unless_they_are_markup(self):
        with tempfile.TemporaryDirectory() as temp:
            src = Path(temp)
            (src / "locales").mkdir()
            (src / "site.json").write_text(json.dumps({"origin": "https://example.invalid", "default": "en"}))
            words = {"lang.tag": "en", "lang.name": "English", "lang.suggest": "s", "lang.dismiss": "d",
                     "a": 'x < "y" & z', "b.html": "<em>b</em>"}
            (src / "locales/en.json").write_text(json.dumps(words))
            for name in build_site.PAGES:
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
