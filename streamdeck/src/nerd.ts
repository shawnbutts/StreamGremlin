// Nerd Fonts icons on keys (D-081). Stream Deck draws key images without
// web fonts, so the plugin ships each icon's outline as an SVG path
// (cmd/desktop/frontend/tools/nerd-symbols.py writes them from the same
// font the app uses). Loaded the first time a key needs one.
import { readFile } from 'node:fs/promises';
import { promisify } from 'node:util';
import { gunzip } from 'node:zlib';

export interface Glyph {
  /** The outline, in font units, y up. */
  d: string;
  bounds: [number, number, number, number];
  unitsPerEm: number;
}

export type NerdLookup = (name: string) => Glyph | undefined;

const loaded = new Map<string, Promise<NerdLookup>>();

/** The outlines in file, read once; a missing file draws nothing. */
export function loadNerd(file: string): Promise<NerdLookup> {
  let p = loaded.get(file);
  if (!p) {
    p = (async () => {
      try {
        const raw = await promisify(gunzip)(await readFile(file));
        const data = JSON.parse(raw.toString('utf8')) as { unitsPerEm: number; glyphs: Record<string, [string, [number, number, number, number]]> };
        return (name: string) => {
          const g = data.glyphs[name];
          return g ? { d: g[0], bounds: g[1], unitsPerEm: data.unitsPerEm } : undefined;
        };
      } catch {
        return () => undefined;
      }
    })();
    loaded.set(file, p);
  }
  return p;
}
