"""The README is translated into every language the preview speaks, and each
translation keeps the commands, links and sections of the original."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
FENCE = re.compile(r"^```.*?^```", re.M | re.S)
LINK = re.compile(r"""(?:\]\(|href=")([^)"#]+)""")


def default_language() -> str:
    source = (ROOT / "internal/preview/language.go").read_text(encoding="utf-8")
    return re.search(r'const DefaultLanguage = "(\w+)"', source).group(1)


def readmes() -> dict[str, Path]:
    """Each language's README: README.md in the default, README.<code>.md otherwise."""
    default = default_language()
    return {path.stem: ROOT / ("README.md" if path.stem == default else f"README.{path.stem}.md")
            for path in (ROOT / "internal/preview/locales").glob("*.json")}


def links(text: str) -> set[str]:
    """Where a README points, other than at itself in another language or at the homepage."""
    return {target for target in LINK.findall(FENCE.sub("", text))
            if not re.fullmatch(r"README(\.[\w-]+)?\.md", target) and "topophany.com" not in target}


class ReadmeTests(unittest.TestCase):
    def test_every_language_has_a_readme(self):
        found = {path.name for path in ROOT.glob("README*.md")}
        self.assertEqual(found, {path.name for path in readmes().values()})

    def test_translations_keep_the_original(self):
        original = (ROOT / "README.md").read_text(encoding="utf-8")
        names = [path.name for path in readmes().values()]
        for code, path in readmes().items():
            text = path.read_text(encoding="utf-8")
            with self.subTest(language=code):
                self.assertEqual(FENCE.findall(text), FENCE.findall(original), "commands differ")
                self.assertEqual(links(text), links(original), "links differ")
                self.assertEqual(re.findall(r"^#+ ", text, re.M), re.findall(r"^#+ ", original, re.M), "sections differ")
                for name in names:
                    if name != path.name:
                        self.assertIn(f'<a href="{name}">', text, f"no link to {name}")

    def test_each_readme_shows_the_tui_in_its_language(self):
        shown = {}
        for code, path in readmes().items():
            stills = re.findall(r"docs/assets/(\w+)\.([\w-]+)\.png", path.read_text(encoding="utf-8"))
            with self.subTest(language=code):
                self.assertTrue(stills, "no capture of the TUI")
                for name, lang in stills:
                    self.assertEqual(lang, code, f"{name} is captured in another language")
                    self.assertTrue((ROOT / f"docs/assets/{name}.{lang}.png").is_file(), f"no {name}.{lang}.png; run make captures")
            shown[code] = sorted(name for name, _ in stills)
        self.assertEqual(len(set(map(tuple, shown.values()))), 1, shown)


if __name__ == "__main__":
    unittest.main()
