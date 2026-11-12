// What a key shows (M7 slice 5): the button's colour, icon (Tabler, or
// Nerd Fonts from its outline) and label, or
// why it can't, never something that looks working when it isn't. Images
// are SVG at 144×144, which Stream Deck scales for every model.
import tabler from '../node_modules/@tabler/icons/tabler-nodes-outline.json';
import type { DeckButton } from './engine';
import type { Glyph, NerdLookup } from './nerd';

type Node = [string, Record<string, string>];
const icons = tabler as unknown as Record<string, Node[]>;

/** The app's button colours (Live), so a key matches its button. */
const COLORS: Record<string, string> = {
  red: '#d9534f',
  orange: '#e8892b',
  yellow: '#e6c229',
  green: '#4caf50',
  teal: '#26a69a',
  blue: '#3e8ed0',
  purple: '#9567d4',
  pink: '#e06ba8',
};

export type DeckState = 'ok' | 'unreachable' | 'unpaired';

export interface KeyView {
  title: string;
  image: string;
}

const svg = (body: string) =>
  'data:image/svg+xml,' +
  encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="144" viewBox="0 0 144 144">${body}</svg>`);

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);

/** A plain key with a mark in the middle (?, !, …). */
function plain(mark: string, color = '#8a8a96'): string {
  return svg(
    `<rect width="144" height="144" rx="16" fill="#1c1c22"/><text x="72" y="74" font-family="Helvetica, Arial, sans-serif" font-size="56" font-weight="700" fill="${color}" text-anchor="middle" dominant-baseline="middle">${esc(mark)}</text>`,
  );
}

/** How to draw a button's icon at a size and height, if it can be drawn. */
function iconDrawer(icon: string, nerd?: NerdLookup): ((size: number, y: number) => string) | undefined {
  if (icon.startsWith('tabler:')) {
    const nodes = icons[icon.slice('tabler:'.length)];
    return nodes && ((size, y) => drawIcon(nodes, size, y));
  }
  if (icon.startsWith('nf:')) {
    const g = nerd?.(icon.slice('nf:'.length));
    return g && ((size, y) => drawGlyph(g, size, y));
  }
  return undefined;
}

/** A font glyph's outline, fitted into a square and flipped (fonts draw y up). */
function drawGlyph(g: Glyph, size: number, y: number): string {
  const [x0, y0, x1, y1] = g.bounds;
  const w = Math.max(1, x1 - x0);
  const h = Math.max(1, y1 - y0);
  const s = size / Math.max(w, h);
  const tx = 72 - (w * s) / 2;
  const ty = y + (size - h * s) / 2 + h * s;
  const n = (v: number) => Number(v.toFixed(4));
  return `<path transform="translate(${n(tx)} ${n(ty)}) scale(${n(s)} -${n(s)}) translate(${-x0} ${-y0})" fill="#ffffff" d="${esc(g.d)}"/>`;
}

function drawIcon(nodes: Node[], size: number, y: number): string {
  const s = size / 24;
  const parts = nodes
    .map(([tag, attrs]) => {
      if (!/^(path|circle|rect|line|polyline|polygon|ellipse)$/.test(tag)) return '';
      const a = Object.entries(attrs)
        .filter(([k]) => /^[a-z-]+$/.test(k) && k !== 'key')
        .map(([k, v]) => `${k}="${esc(String(v))}"`)
        .join(' ');
      return `<${tag} ${a}/>`;
    })
    .join('');
  return `<g transform="translate(${72 - size / 2} ${y}) scale(${s})" fill="none" stroke="#ffffff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${parts}</g>`;
}

/** Breaks a label into up to three short lines, for the key's title. */
export function titleLines(label: string, width = 10): string {
  const lines: string[] = [];
  let line = '';
  for (const word of label.trim().split(/\s+/)) {
    const w = word.length > width ? `${word.slice(0, width - 1)}…` : word;
    if (!line) line = w;
    else if (line.length + 1 + w.length <= width) line += ` ${w}`;
    else {
      lines.push(line);
      line = w;
    }
  }
  if (line) lines.push(line);
  if (lines.length > 3) {
    lines.length = 3;
    lines[2] = `${lines[2].slice(0, width - 1)}…`;
  }
  return lines.join('\n');
}

export function keyView(o: { state: DeckState; buttonId?: string; buttons?: DeckButton[] }, nerd?: NerdLookup): KeyView {
  if (o.state === 'unreachable') return { title: 'Not\nrunning', image: plain('?') };
  if (o.state === 'unpaired') return { title: 'Pair\nfirst', image: plain('!', '#e6c229') };
  if (!o.buttonId) return { title: 'Pick a\nbutton', image: plain('…') };
  const b = o.buttons?.find((x) => x.id === o.buttonId);
  if (!b) return { title: 'Button\ngone', image: plain('?') };

  const icon = iconDrawer(b.icon, nerd);
  const color = COLORS[b.color] ?? '#55555f';
  const onlyIcon = !!icon && b.hideLabel;
  const body =
    `<rect width="144" height="144" rx="16" fill="#1c1c22"/>` +
    `<rect y="124" width="144" height="20" fill="${color}"/>` +
    (icon ? icon(onlyIcon ? 80 : 56, onlyIcon ? 24 : 14) : '');
  const dim = b.starts === 0 ? `<g opacity="0.4">${body}</g>` : body;
  return { title: onlyIcon ? '' : titleLines(b.label), image: svg(dim) };
}
