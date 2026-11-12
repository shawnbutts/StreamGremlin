"""Builds the Nerd Fonts icon set for button icons (D-076, ADR-0040).

From the Symbols Only font and glyphnames.json of a Nerd Fonts release
(https://github.com/ryanoasis/nerd-fonts/releases), it keeps the icon sets
that suit a button, subsets the font to them, and writes:

  src/assets/nerd/symbols.woff2      the font
  src/assets/nerd/glyphs.json        {name: codepoint}, like "md-microphone"
  ../../../streamdeck/com.streamgremlin.streamdeck.sdPlugin/icons/nerd-paths.json.gz
                                     each icon as an SVG path, for Stream
                                     Deck keys, which can't use the font

Needs fonttools and brotli. Run from cmd/desktop/frontend:

  python3 tools/nerd-symbols.py SymbolsNerdFont-Regular.ttf glyphnames.json
"""

import gzip
import json
import sys

from fontTools import subset
from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.ttLib import TTFont

# Material Design, Font Awesome (and its extension), Devicons, Codicons,
# Octicons, Weather Icons and Linux logos. Left out: Powerline and other
# terminal glyphs, and Seti's file-type icons.
KEEP = ("md", "fa", "fae", "dev", "cod", "oct", "weather", "linux")


def main(font_path: str, names_path: str) -> None:
    with open(names_path, encoding="utf-8") as f:
        names = json.load(f)
    meta = names.pop("METADATA", {})
    glyphs = {k: int(v["code"], 16) for k, v in sorted(names.items()) if k.split("-")[0] in KEEP}
    font = TTFont(font_path)
    have = set(font.getBestCmap())
    glyphs = {k: c for k, c in glyphs.items() if c in have}

    opts = subset.Options()
    opts.flavor = "woff2"
    opts.layout_features = []
    opts.name_IDs = ["*"]  # keep the font's names and licence notice
    opts.notdef_outline = True
    sub = subset.Subsetter(opts)
    sub.populate(unicodes=sorted(set(glyphs.values())))
    sub.subset(font)
    font.flavor = "woff2"
    font.save("src/assets/nerd/symbols.woff2")
    with open("src/assets/nerd/glyphs.json", "w", encoding="utf-8") as f:
        json.dump({"version": meta.get("version", ""), "glyphs": glyphs}, f, separators=(",", ":"))
    write_paths(TTFont("src/assets/nerd/symbols.woff2"), glyphs, meta.get("version", ""))
    print(f"{len(glyphs)} icons from Nerd Fonts {meta.get('version', '?')}")


def write_paths(font: TTFont, glyphs: dict, version: str) -> None:
    """Each icon's outline in font units (y up), with its bounds."""
    cmap, gs = font.getBestCmap(), font.getGlyphSet()
    out = {}
    for name, cp in glyphs.items():
        g = cmap.get(cp)
        if g is None:
            continue
        bounds = BoundsPen(gs)
        gs[g].draw(bounds)
        if bounds.bounds is None:
            continue
        pen = SVGPathPen(gs, lambda v: "%.0f" % v)
        gs[g].draw(pen)
        out[name] = [pen.getCommands(), [round(v) for v in bounds.bounds]]
    data = {"version": version, "unitsPerEm": font["head"].unitsPerEm, "glyphs": out}
    path = "../../../streamdeck/com.streamgremlin.streamdeck.sdPlugin/icons/nerd-paths.json.gz"
    with gzip.GzipFile(path, "wb", mtime=0) as f:  # mtime 0: the same input gives the same file
        f.write(json.dumps(data, separators=(",", ":"), sort_keys=True).encode())


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
