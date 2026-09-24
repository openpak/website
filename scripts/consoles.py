#!/usr/bin/env python3
"""Normalise a console logo into web/static/consoles/<slug>.svg and <slug>-dark.svg.

    scripts/consoles.py <source.svg> <slug> [<source.svg> <slug> ...]

The art is cropped to what it actually draws (measured by rendering it with
rsvg-convert and trimming with ImageMagick, because viewBoxes lie) and scaled
into the 36x24 slot, centred. Editor metadata and namespaced attributes are
dropped; CSS classes from a <style> block are inlined, since the result is
rewrapped and the block would no longer apply.

Colours follow CREDITS.md: against the tile each theme draws the logo on, a
colour below 3:1 (4.5:1 for a near-neutral, which goes grey) is moved in
lightness, hue kept, until it passes: darker for the light file, lighter for
the dark one. Unfilled shapes default to black, so the wrapper sets that
default explicitly and it is adjusted like any other colour.
"""
import colorsys
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from pathlib import Path

SVG, XLINK = "http://www.w3.org/2000/svg", "http://www.w3.org/1999/xlink"
ET.register_namespace("", SVG)
ET.register_namespace("xlink", XLINK)
OUT = Path(__file__).resolve().parent.parent / "web/static/consoles"
BOX_W, BOX_H, PAD = 36.0, 24.0, 0.5
# The console tile's background (--paper-2) in each theme.
TILES = {"": "#f1f1f4", "-dark": "#181a21"}
COLOR_PROPS = ("fill", "stroke", "stop-color")
NAMED = {"black": "#000000", "white": "#ffffff"}


def lum(hexc):
    r, g, b = (int(hexc[i:i + 2], 16) / 255 for i in (1, 3, 5))
    f = lambda c: c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)


def contrast(a, b):
    la, lb = sorted((lum(a), lum(b)), reverse=True)
    return (la + 0.05) / (lb + 0.05)


def norm(c):
    c = NAMED.get(c.strip().lower(), c.strip().lower())
    if re.fullmatch(r"#[0-9a-f]{3}", c):
        c = "#" + "".join(ch * 2 for ch in c[1:])
    return c if re.fullmatch(r"#[0-9a-f]{6}", c) else None


def adjust(c, tile, lighter):
    c = norm(c)
    if c is None:
        return None
    r, g, b = (int(c[i:i + 2], 16) / 255 for i in (1, 3, 5))
    h, l, s = colorsys.rgb_to_hls(r, g, b)
    neutral = s < 0.15
    need = 4.5 if neutral else 3.0
    if neutral:
        s = 0.0
    out = c
    while contrast(out, tile) < need and (l < 1.0 if lighter else l > 0.0):
        l = min(1.0, l + 0.01) if lighter else max(0.0, l - 0.01)
        out = "#%02x%02x%02x" % tuple(round(v * 255) for v in colorsys.hls_to_rgb(h, l, s))
    return out


def bbox(src, vb):
    """The drawn area, in the source's user units."""
    width = 2000
    with tempfile.NamedTemporaryFile(suffix=".png") as png:
        subprocess.run(["rsvg-convert", "-w", str(width), "-o", png.name, str(src)], check=True)
        geo = subprocess.run(["magick", png.name, "-format", "%@", "info:"],
                             check=True, capture_output=True, text=True).stdout
    w, h, x, y = map(float, re.fullmatch(r"(\d+)x(\d+)\+(\d+)\+(\d+)", geo.strip()).groups())
    k = vb[2] / width
    return vb[0] + x * k, vb[1] + y * k, w * k, h * k


def number(v):
    return float(re.match(r"[-\d.]+", v).group())


def clean(el, classes):
    """Drop editor elements and foreign attributes; inline class styles."""
    for child in list(el):
        ns = child.tag.split("}")[0][1:] if child.tag.startswith("{") else SVG
        if ns != SVG or child.tag in (f"{{{SVG}}}metadata", f"{{{SVG}}}style", f"{{{SVG}}}title"):
            el.remove(child)
        else:
            clean(child, classes)
    for k in list(el.attrib):
        if k.startswith("{") and not k.startswith(f"{{{XLINK}}}"):
            del el.attrib[k]
    for cls in el.attrib.pop("class", "").split():
        for prop, val in classes.get(cls, {}).items():
            el.set(prop, val)


def recolor(el, fn):
    for k in COLOR_PROPS:
        if k in el.attrib and (new := fn(el.get(k))):
            el.set(k, new)
    if "style" in el.attrib:
        def sub(m):
            new = fn(m.group(2))
            return f"{m.group(1)}:{new}" if new else m.group(0)
        el.set("style", re.sub(r"(fill|stroke|stop-color)\s*:\s*(#[0-9a-fA-F]{3,6}|black|white)", sub, el.get("style")))
    for child in el:
        recolor(child, fn)


def build(src, slug):
    root = ET.parse(src).getroot()
    if "viewBox" in root.attrib:
        vb = [float(v) for v in re.split(r"[\s,]+", root.get("viewBox").strip())]
    else:
        vb = [0.0, 0.0, number(root.get("width")), number(root.get("height"))]
    bx, by, bw, bh = bbox(src, vb)
    classes = {}
    for style in root.iter(f"{{{SVG}}}style"):
        for name, body in re.findall(r"\.([\w-]+)\s*\{([^}]*)\}", style.text or ""):
            classes[name] = dict(p.split(":", 1) for p in (d.strip() for d in body.split(";")) if ":" in p)
            classes[name] = {k.strip(): v.strip() for k, v in classes[name].items()}
    clean(root, classes)
    s = min((BOX_W - 2 * PAD) / bw, (BOX_H - 2 * PAD) / bh)
    tx, ty = (BOX_W - bw * s) / 2 - bx * s, (BOX_H - bh * s) / 2 - by * s
    for suffix, tile in TILES.items():
        out = ET.Element(f"{{{SVG}}}svg", {"viewBox": f"0 0 {BOX_W:g} {BOX_H:g}", "aria-hidden": "true"})
        g = ET.SubElement(out, f"{{{SVG}}}g", {"transform": f"translate({tx:.3f},{ty:.3f}) scale({s:.5f})", "fill": "#000000"})
        for child in root:
            g.append(ET.fromstring(ET.tostring(child)))
        recolor(g, lambda c, tile=tile, suffix=suffix: adjust(c, tile, lighter=bool(suffix)))
        (OUT / f"{slug}{suffix}.svg").write_text(ET.tostring(out, encoding="unicode") + "\n")
        print(f"{slug}{suffix}.svg")


if __name__ == "__main__":
    args = sys.argv[1:]
    if not args or len(args) % 2:
        sys.exit(__doc__)
    for src, slug in zip(args[::2], args[1::2]):
        build(Path(src), slug)
