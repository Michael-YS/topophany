#!/usr/bin/env python3
"""Paint the TUI on each device's screen it is laid out for, and set each in its device.

TestDeviceShots (internal/tui) records each shot as the TUI draws it on a
device's screen: a still in the tour's format (<name>.json) beside the device
it was drawn for (<name>.device.json). The homepage's terminal renderer
(site/src/js/term.js) paints each in a headless Chromium at the device's own
pixels, a cell 7 points wide and 14 tall, so a region the system reserves (the
Dynamic Island, iPhone Duo's clock column and its crease) lands where the
device has it. Each screen is then set in a frame drawn here: an iPhone, an
iPad, and iPhone Duo shut, open and part folded. The folded poses are rendered
in 3D by duo3d.py from the iPhone Duo simulator kit
(github.com/WhitenWhiten/iphone-duo-simulator-skill), passed with --duo-kit;
the frames are this script's own drawing, not Apple's art.

Writes, into --out: <name>.screen.png (the screen at the device's pixels),
<name>.png (in its device), duo-poses.png (iPhone Duo's poses side by side)
and index.html, a gallery of them all; with --assets, the strip
docs/DEVICES.md shows too.

Needs Pillow and numpy, a Chromium (Playwright's headless shell will do), and
the kit. Usage:
  python3 scripts/device_shots.py --duo-kit PATH [--browser PATH] [--out DIR]
"""
from __future__ import annotations

import argparse
import glob
import html
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SITE = ROOT / "site" / "src"
sys.path.insert(0, str(Path(__file__).resolve().parent))
import capture  # noqa: E402  (the painting page and server are capture.py's)

INK = (10, 16, 22)  # the TUI's background, cInk
LONGEST = 2400      # a mock-up's longest side, px

# The frames' finishes: iPhone Duo in Night Sky, the others in a dark titanium.
NAVY = ((40, 47, 66), (20, 24, 35), (78, 88, 116))      # body top, body foot, rim light
TITANIUM = ((58, 60, 66), (30, 31, 35), (120, 123, 130))
GLASS = (6, 7, 9)


def browser(given: str | None) -> str:
    """A Chromium that can paint headless: capture.py's, or Playwright's on macOS."""
    found = sorted(glob.glob(os.path.expanduser(
        "~/Library/Caches/ms-playwright/chromium_headless_shell-*/chrome-headless-shell-*/chrome-headless-shell")))
    if not given and found and not os.environ.get("CHROME"):
        return found[-1]
    return capture.browser(given)


# --- painting ---------------------------------------------------------------

def paint_all(chrome: str, shots: Path, work: Path) -> dict[str, Path]:
    """Paints every shot's screen at its device's pixels, padded to the whole
    window with the TUI's ink; returns each shot's PNG by name."""
    from PIL import Image
    for name in ("js/term.js", "css/site.css"):
        shutil.copy(SITE / name, shots / Path(name).name)
    server = capture.serve(shots)
    out = {}
    try:
        for still in sorted(shots.glob("*.json")):
            if still.name.endswith(".device.json"):
                continue
            name = still.stem
            dev = json.loads((shots / f"{name}.device.json").read_text())
            cols, rows = json.loads(still.read_text())["size"]
            cw = round(dev["cell"][0] * dev["scale"])
            width, height = cols * cw, rows * cw * 2
            page = f"{name}.html"
            (shots / page).write_text(capture.PAGE.format(lang="en", width=width, capture=still.name), encoding="utf-8")
            frames = work / "frames" / name
            frames.mkdir(parents=True)
            capture.paint(chrome, dict(os.environ), server, page, (width, height), frames)
            painted = Image.open(frames / "0000.png").convert("RGB")
            full = (round(dev["size"][0] * dev["scale"]), round(dev["size"][1] * dev["scale"]))
            screen = Image.new("RGB", full, INK)
            screen.paste(painted, (0, 0))
            out[name] = frames / "screen.png"
            screen.save(out[name])
            print(f"painted {name} ({cols}×{rows} cells, {full[0]}×{full[1]} px)")
    finally:
        server.shutdown()
    return out


# --- frames -----------------------------------------------------------------

def body_art(size, box, radius, finish, hole, hole_radius, rim=14):
    """A device's face: a body with a lit rim, black glass inside it and a
    transparent screen hole, RGBA."""
    import numpy as np
    from PIL import Image, ImageDraw
    w, h = size
    top, foot, light = finish
    grad = np.linspace(0, 1, h)[:, None]
    rgb = np.empty((h, w, 3), np.uint8)
    for c in range(3):
        rgb[..., c] = (top[c] + (foot[c] - top[c]) * grad).astype(np.uint8)
    art = Image.fromarray(rgb, "RGB").convert("RGBA")
    shape = Image.new("L", size, 0)
    ImageDraw.Draw(shape).rounded_rectangle(box, radius=radius, fill=255)
    rim_light = Image.new("RGBA", size, light + (255,))
    inner = Image.new("L", size, 0)
    ImageDraw.Draw(inner).rounded_rectangle([box[0] + 3, box[1] + 3, box[2] - 3, box[3] - 3], radius=radius - 3, fill=255)
    ring = Image.new("L", size, 0)
    ring.paste(shape, (0, 0))
    ring = Image.fromarray(np.minimum(np.array(shape), 255 - np.array(inner)), "L")
    art.paste(rim_light, (0, 0), ring.point(lambda v: v * 120 // 255))
    glass = Image.new("L", size, 0)
    ImageDraw.Draw(glass).rounded_rectangle([box[0] + rim, box[1] + rim, box[2] - rim, box[3] - rim],
                                            radius=max(1, radius - rim), fill=255)
    art.paste(Image.new("RGBA", size, GLASS + (255,)), (0, 0), glass)
    alpha = np.array(shape)
    cut = Image.new("L", size, 0)
    ImageDraw.Draw(cut).rounded_rectangle(hole, radius=hole_radius, fill=255)
    alpha = np.minimum(alpha, 255 - np.array(cut))
    art.putalpha(Image.fromarray(alpha.astype(np.uint8), "L"))
    return art


def pill(art, box, color=GLASS, lens=None):
    """Hardware over the glass: the Dynamic Island, a camera; a lens glint at lens."""
    from PIL import ImageDraw
    d = ImageDraw.Draw(art)
    r = min(box[2] - box[0], box[3] - box[1]) / 2
    d.rounded_rectangle(box, radius=r, fill=color + (255,))
    if lens:
        x, y, s = lens
        d.ellipse([x - s, y - s, x + s, y + s], fill=(22, 26, 40, 255))
        d.ellipse([x - s * .45, y - s * .45, x + s * .1, y + s * .1], fill=(44, 52, 82, 255))


def framed(screen_png, dev, kit):
    """The screen set in its device: flat on the studio's background, or
    folded in 3D for iPhone Duo's book and laptop poses."""
    from PIL import Image
    sys.path.insert(0, str(kit / "scripts"))
    import duo3d
    import duo_mockup
    screen = Image.open(screen_png).convert("RGB")
    sw, sh = screen.size
    s = dev["scale"]
    if dev["model"] == "iPhone Duo":
        if dev["pose"] == "cover":
            pad, bez = 58, 46
            size = (sw + 2 * (pad + bez), sh + 2 * (pad + bez))
            hole = [pad + bez, pad + bez, pad + bez + sw, pad + bez + sh]
            art = body_art(size, [pad, pad, size[0] - pad, size[1] - pad], 150, NAVY, hole, 112)
            # The camera goes over the pasted screen: in the art it would
            # split the hole, and paste_screen keeps only its widest runs.
            face = duo_mockup.paste_screen(art, screen)
            cam = dev["occlusions"][1]
            cx, cy = hole[0] + (cam["X"] + cam["W"] / 2) * s, hole[1] + (cam["Y"] + cam["H"] / 2) * s
            pill(face, [cx - cam["W"] * s / 2, cy - cam["H"] * s / 2, cx + cam["W"] * s / 2, cy + cam["H"] * s / 2],
                 lens=(cx, cy, cam["W"] * s * .3))
            return duo_mockup.finish(face, pad=170)
        upright = sw < sh
        if upright:
            screen = screen.transpose(Image.ROTATE_90)
        # The inner display's face, as duo3d.py measures Apple's art: the
        # device at (57.79, 57.90), 2976.4×2130.5 in a 3093×2247 picture.
        x0, y0 = 57.79, 57.90
        box = [x0, y0, x0 + 2976.4, y0 + 2130.5]
        hx, hy = round(x0 + (2976.4 - 2853) / 2), round(y0 + (2130.5 - 2007) / 2)
        art = body_art((3093, 2247), box, 160, NAVY, [hx, hy, hx + 2853, hy + 2007], 118)
        comp = duo_mockup.paste_screen(art, screen)
        if dev["angle"] >= 180:
            out = comp
            if upright:
                out = out.transpose(Image.ROTATE_270)
            return duo_mockup.finish(out, pad=170)
        back = body_art((3056, 2194), [34.3, 30.5, 3010.7, 2161.0], 160, NAVY, [0, 0, 0, 0], 1)
        folded = duo3d.render(comp, back, deg=dev["angle"])
        if upright:
            folded = folded.transpose(Image.ROTATE_270)
        return duo_mockup.finish(folded, pad=180)
    # An iPhone or an iPad: an even bezel, the screen's corners round.
    phone = dev["model"].startswith("iPhone")
    bez = round((7 if phone else 18) * s)
    pad = 60
    corner = (55 if phone else 18) * s
    size = (sw + 2 * (pad + bez), sh + 2 * (pad + bez))
    hole = [pad + bez, pad + bez, pad + bez + sw, pad + bez + sh]
    art = body_art(size, [pad, pad, size[0] - pad, size[1] - pad], corner + bez, TITANIUM, hole, corner, rim=10)
    face = duo_mockup.paste_screen(art, screen)
    for o in dev["occlusions"] or []:
        pill(face, [hole[0] + o["X"] * s, hole[1] + o["Y"] * s, hole[0] + (o["X"] + o["W"]) * s, hole[1] + (o["Y"] + o["H"]) * s])
    if not phone:
        # The front camera, centred on the long edge in landscape.
        cx = hole[0] + sw / 2 if sw > sh else hole[0] - bez / 2
        cy = hole[1] - bez / 2 if sw > sh else hole[1] + sh / 2
        pill(face, [cx - 9, cy - 9, cx + 9, cy + 9], lens=(cx, cy, 6))
    return duo_mockup.finish(face, pad=150)


# The same moment on iPhone Duo in each pose, as it unfolds.
STRIP = ["duo-cover-overview", "duo-open-overview", "duo-book-overview", "duo-upright-overview", "duo-laptop-overview"]


def strip(out: Path) -> Path | None:
    """The Duo's poses side by side, each labelled, at one height."""
    from PIL import Image, ImageDraw, ImageFont
    shots = [out / f"{n}.png" for n in STRIP]
    if not all(p.exists() for p in shots):
        return None
    h = 900
    imgs = []
    for p in shots:
        im = Image.open(p).convert("RGB")
        imgs.append(im.resize((round(im.width * h / im.height), h), Image.LANCZOS))
    label_h = 70
    canvas = Image.new("RGB", (sum(i.width for i in imgs), h + label_h), (244, 245, 248))
    draw = ImageDraw.Draw(canvas)
    try:
        font = ImageFont.truetype("/System/Library/Fonts/SFNS.ttf", 30)
    except OSError:
        font = ImageFont.load_default()
    x = 0
    for name, im in zip(STRIP, imgs):
        canvas.paste(im, (x, 0))
        pose = json.loads((out / f"{name}.device.json").read_text())
        text = f"{pose['pose']} · {pose['about']}"
        tw = draw.textlength(text, font=font)
        draw.text((x + (im.width - tw) / 2, h + 12), text, fill=(60, 64, 72), font=font)
        x += im.width
    path = out / "duo-poses.png"
    canvas.save(path)
    return path


# --- the gallery --------------------------------------------------------------

GALLERY = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Topophany on devices</title>
<style>
body {{ margin: 0; padding: 32px 40px 64px; background: #f4f5f8; font: 15px/1.5 -apple-system, system-ui, sans-serif; color: #1c2128; }}
h1 {{ font-size: 26px; margin: 0 0 4px; }} h2 {{ margin: 40px 0 12px; font-size: 20px; }}
p.lead {{ color: #555; max-width: 70em; }}
.grid {{ display: grid; grid-template-columns: repeat(auto-fill, minmax(360px, 1fr)); gap: 28px; }}
figure {{ margin: 0; background: #fff; border-radius: 14px; padding: 14px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }}
figure img {{ width: 100%; display: block; border-radius: 8px; }}
figcaption {{ margin-top: 10px; }} figcaption b {{ display: block; }} figcaption span {{ color: #666; font-size: 13px; }}
figcaption a {{ font-size: 12px; color: #0a7; }}
</style></head><body>
<h1>Topophany on devices</h1>
<p class="lead">The terminal UI laid out for each screen, half an hour into the preview's transit outage. Each picture links to the screen at the device's own pixels.</p>
{poses}
{sections}
</body></html>
"""


def gallery(out: Path, shots: list[tuple[str, dict]]) -> None:
    sections, by_model = [], {}
    for name, dev in shots:
        by_model.setdefault(dev["model"], []).append((name, dev))
    for model, items in by_model.items():
        figs = "".join(
            f'<figure><a href="{name}.screen.png"><img src="{name}.png" alt="{html.escape(dev["caption"])}"></a>'
            f'<figcaption><b>{html.escape(dev["pose"])} · {html.escape(dev["about"])}</b>'
            f'<span>{html.escape(dev["caption"])}</span><br>'
            f'<a href="{name}.screen.png">{dev["cells"][0]}×{dev["cells"][1]} cells · {html.escape(dev["layout"])} layout</a>'
            f'</figcaption></figure>'
            for name, dev in items)
        sections.append(f"<h2>{html.escape(model)}</h2><div class=\"grid\">{figs}</div>")
    poses = ""
    if (out / "duo-poses.png").exists():
        poses = ('<h2>iPhone Duo, unfolding</h2><figure><a href="duo-poses.png"><img src="duo-poses.png" '
                 'alt="The same moment on iPhone Duo shut, open, part folded like a book, upright and like a laptop"></a>'
                 '<figcaption><span>The same moment through every pose: the tab, the selection and the map\'s '
                 'window carry over, and bending reflows nothing.</span></figcaption></figure>')
    (out / "index.html").write_text(GALLERY.format(poses=poses, sections="".join(sections)), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--duo-kit", type=Path, default=os.environ.get("DUO_KIT"),
                        help="a checkout of github.com/WhitenWhiten/iphone-duo-simulator-skill (default: $DUO_KIT)")
    parser.add_argument("--browser", help="Chromium to paint with (default: Playwright's, $CHROME, or one on PATH)")
    parser.add_argument("--out", type=Path, default=ROOT / ".device-shots", help="where to write (default: .device-shots)")
    parser.add_argument("--only", help="paint only the shots whose names contain this")
    parser.add_argument("--assets", action="store_true", help="also write the poses strip docs/DEVICES.md shows")
    args = parser.parse_args()
    if not args.duo_kit or not (args.duo_kit / "scripts" / "duo3d.py").exists():
        parser.error("--duo-kit must be a checkout of the iPhone Duo simulator kit")
    chrome = browser(args.browser)
    args.out.mkdir(parents=True, exist_ok=True)
    from PIL import Image
    with tempfile.TemporaryDirectory() as temp:
        work = Path(temp)
        shots = work / "shots"
        shots.mkdir()
        subprocess.run(["go", "test", "-count=1", "-run", "^TestDeviceShots$", "./internal/tui"], cwd=ROOT, check=True,
                       env={**os.environ, "TOPO_DEVICE_SHOTS": str(shots)})
        if args.only:
            for f in shots.glob("*.json"):
                if args.only not in f.name:
                    f.unlink()
        screens = paint_all(chrome, shots, work)
        done = []
        for name, png in sorted(screens.items()):
            dev = json.loads((shots / f"{name}.device.json").read_text())
            shutil.copy(png, args.out / f"{name}.screen.png")
            img = framed(png, dev, args.duo_kit.resolve())
            img.thumbnail((LONGEST, LONGEST), Image.LANCZOS)
            img.save(args.out / f"{name}.png")
            done.append((name, dev))
            print(f"wrote {args.out / (name + '.png')}")
    # The gallery lists every shot written so far, this run's and earlier
    # ones, each device's poses in the order they unfold.
    for name, dev in done:
        (args.out / f"{name}.device.json").write_text(json.dumps(dev))
    every = [(side.name[:-len(".device.json")], json.loads(side.read_text())) for side in args.out.glob("*.device.json")]
    order = ["iphone", "iphone-landscape", "duo-cover", "duo-open", "duo-book", "duo-upright", "duo-laptop", "ipad", "ipad-portrait"]
    every.sort(key=lambda s: (order.index(s[1]["id"]) if s[1]["id"] in order else 99, s[0]))
    if made := strip(args.out):
        print(f"wrote {made}")
        if args.assets:
            # docs/DEVICES.md shows the strip: smaller, in a palette, as the
            # README's stills are kept small.
            doc = Image.open(made).convert("RGB")
            doc.thumbnail((2400, 2400), Image.LANCZOS)
            path = ROOT / "docs" / "assets" / "duo-poses.png"
            doc.quantize(colors=128, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).save(path, optimize=True)
            print(f"wrote {path.relative_to(ROOT)}")
    gallery(args.out, every)
    print(f"gallery: {args.out / 'index.html'}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
