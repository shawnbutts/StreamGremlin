"""Builds the Nerd Fonts icon set for button icons (D-076, ADR-0040).

From the Symbols Only font and glyphnames.json of a Nerd Fonts release
(https://github.com/ryanoasis/nerd-fonts/releases), it keeps the icon sets
that suit a button, subsets the font to them, and writes:

  src/assets/nerd/symbols.woff2      the font
  src/assets/nerd/glyphs.json        {name: codepoint}, like "md-microphone"

Needs fonttools and brotli. Run from cmd/desktop/frontend:

  python3 tools/nerd-symbols.py SymbolsNerdFont-Regular.ttf glyphnames.json
"""

import json
import sys

from fontTools import subset
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
    print(f"{len(glyphs)} icons from Nerd Fonts {meta.get('version', '?')}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
