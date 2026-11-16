// What a key shows (M7 slice 5): the button's colour, icon (Tabler, or
// Nerd Fonts from its outline) and label, or
// why it can't, never something that looks working when it isn't. Images
// are SVG at 144×144, which Stream Deck scales for every model.
import tabler from '../node_modules/@tabler/icons/tabler-nodes-outline.json';
import { switchKey, type Change, type DeckButton, type DeckView } from './engine';
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
  /** Why a press can't work, for the log. */
  why?: string;
}

const svg = (body: string) =>
  'data:image/svg+xml,' +
  encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="144" height="144" viewBox="0 0 144 144">${body}</svg>`);

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);

/** A plain key with a mark in the middle (?, !, …). */
function plain(mark: string, color = '#8a8a96'): string {
  return svg(
    `<rect width="144" height="144" rx="16" fill="${PLAIN}"/><text x="72" y="74" font-family="Helvetica, Arial, sans-serif" font-size="56" font-weight="700" fill="${color}" text-anchor="middle" dominant-baseline="middle">${esc(mark)}</text>`,
  );
}

/** Dark ink on every button colour (at least 5:1, as on Live); light on the plain key. */
const INK = '#111114';
const PLAIN = '#1c1c22';

type Drawer = (size: number, y: number, ink: string) => string;

/** How to draw a button's icon at a size and height, if it can be drawn. */
function iconDrawer(icon: string, nerd?: NerdLookup): Drawer | undefined {
  if (icon.startsWith('tabler:')) {
    const nodes = icons[icon.slice('tabler:'.length)];
    return nodes && ((size, y, ink) => drawIcon(nodes, size, y, ink));
  }
  if (icon.startsWith('nf:')) {
    const g = nerd?.(icon.slice('nf:'.length));
    return g && ((size, y, ink) => drawGlyph(g, size, y, ink));
  }
  return undefined;
}

/** The label in up to three lines: under an icon, or in the middle. */
function drawLabel(label: string, ink: string, underIcon: boolean): string {
  const lines = titleLines(label).split('\n');
  const step = 25;
  const first = underIcon ? 134 - (lines.length - 1) * step : 72 - (lines.length * step) / 2 + 18;
  const spans = lines.map((l, i) => `<tspan x="72" y="${first + i * step}">${esc(l)}</tspan>`).join('');
  return `<text font-family="Helvetica, Arial, sans-serif" font-size="22" font-weight="700" fill="${ink}" text-anchor="middle">${spans}</text>`;
}

/** A font glyph's outline, fitted into a square and flipped (fonts draw y up). */
function drawGlyph(g: Glyph, size: number, y: number, ink: string): string {
  const [x0, y0, x1, y1] = g.bounds;
  const w = Math.max(1, x1 - x0);
  const h = Math.max(1, y1 - y0);
  const s = size / Math.max(w, h);
  const tx = 72 - (w * s) / 2;
  const ty = y + (size - h * s) / 2 + h * s;
  const n = (v: number) => Number(v.toFixed(4));
  return `<path transform="translate(${n(tx)} ${n(ty)}) scale(${n(s)} -${n(s)}) translate(${-x0} ${-y0})" fill="${ink}" d="${esc(g.d)}"/>`;
}

function drawIcon(nodes: Node[], size: number, y: number, ink: string): string {
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
  return `<g transform="translate(${72 - size / 2} ${y}) scale(${s})" fill="none" stroke="${ink}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${parts}</g>`;
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

  // The whole key is the button's colour, as on Live (D-076). Stream Deck's
  // own title stays empty: the label is drawn here, in ink that reads on it.
  const icon = iconDrawer(b.icon, nerd);
  const fill = COLORS[b.color];
  const ink = fill ? INK : '#ffffff';
  const onlyIcon = !!icon && b.hideLabel;
  const body =
    `<rect width="144" height="144" rx="16" fill="${fill ?? PLAIN}"/>` +
    (icon ? icon(onlyIcon ? 80 : 56, onlyIcon ? 32 : 10, ink) : '') +
    (onlyIcon ? '' : drawLabel(b.label, ink, !!icon));
  const dim = b.starts === 0 ? `<g opacity="0.4">${body}</g>` : body;
  return { title: '', image: svg(dim) };
}

// Counter, status and switch keys (D-123). Each says why it can't, never
// showing a value or a state it doesn't know.

/** Why a key can't show anything yet, or undefined when it can. */
function notReady(state: DeckState, deck: DeckView | undefined): KeyView | undefined {
  if (state === 'unreachable') return { title: 'Not\nrunning', image: plain('?') };
  if (state === 'unpaired') return { title: 'Pair\nfirst', image: plain('!', '#e6c229') };
  if (!deck) return { title: 'Update\nStreamGremlin', image: plain('?') };
  return undefined;
}

const notChosen = (what: string): KeyView => ({ title: `Choose in\nStreamGremlin`, image: plain('!', '#e6c229'), why: `${what} isn’t chosen for the Stream Deck` });

/** Big centred text on a key, with a small line above and the label below. */
function bigText(big: string, label: string, opts: { fill?: string; top?: string; dim?: boolean } = {}): string {
  const fill = opts.fill ?? PLAIN;
  const ink = opts.fill ? INK : '#ffffff';
  const size = big.length <= 3 ? 64 : big.length <= 5 ? 46 : 32;
  const body =
    `<rect width="144" height="144" rx="16" fill="${fill}"/>` +
    (opts.top ? `<text x="72" y="28" font-family="Helvetica, Arial, sans-serif" font-size="22" font-weight="700" fill="${ink}" text-anchor="middle">${esc(opts.top)}</text>` : '') +
    `<text x="72" y="${opts.top ? 82 : 72}" font-family="Helvetica, Arial, sans-serif" font-size="${size}" font-weight="700" fill="${ink}" text-anchor="middle" dominant-baseline="middle">${esc(big)}</text>` +
    drawLabel(label, ink, true);
  return svg(opts.dim ? `<g opacity="0.4">${body}</g>` : body);
}

/** A number as a key shows it: whole, or to two places. */
function shown(v: number): string {
  const s = Number.isInteger(v) ? String(v) : v.toFixed(2).replace(/\.?0+$/, '');
  return s.length > 7 ? '9999999+' : s;
}

/** A counter key: its variable's value, and what a press does. */
export function counterView(o: { state: DeckState; name?: string; change?: Change; deck?: DeckView }): KeyView {
  const wait = notReady(o.state, o.deck);
  if (wait) return wait;
  if (!o.name) return { title: 'Pick a\ncounter', image: plain('…') };
  const c = o.deck!.counters.find((x) => x.name === o.name);
  if (!c) return notChosen(o.name);
  const top = { add: '+1', take: '−1', reset: 'to 0' }[o.change ?? 'add'];
  if (c.value === null || c.value === undefined) return { title: '', image: bigText('0', c.name, { top }) };
  if (typeof c.value !== 'number') return { title: '', image: bigText('?', c.name, { top }), why: `${c.name} doesn’t hold a number` };
  return { title: '', image: bigText(shown(c.value), c.name, { top }) };
}

export type StatusWhat = 'live' | 'chat' | 'obs' | 'lock';

const CONNECTION: Record<string, { word: string; fill?: string }> = {
  connected: { word: 'On', fill: COLORS.green },
  connecting: { word: '…', fill: COLORS.yellow },
  disconnected: { word: 'Lost', fill: COLORS.red },
  needs_action: { word: 'Fix', fill: COLORS.red },
  off: { word: 'Off' },
};

/** A status key: one thing about the stream, on, off or unknown. */
export function statusView(o: { state: DeckState; what?: StatusWhat; deck?: DeckView }): KeyView {
  const wait = notReady(o.state, o.deck);
  if (wait) return wait;
  const st = o.deck!.status;
  const yesNo = (b: boolean | null, label: string, yes: { word: string; fill: string }, no: string): KeyView =>
    b === null ? { title: '', image: bigText('?', label) } : { title: '', image: b ? bigText(yes.word, label, { fill: yes.fill }) : bigText(no, label) };
  switch (o.what ?? 'live') {
    case 'live':
      return yesNo(st.live, 'Stream', { word: 'LIVE', fill: COLORS.red }, 'Off');
    case 'lock':
      return yesNo(st.chat_locked, 'Chat lock', { word: 'On', fill: COLORS.orange }, 'Off');
    case 'chat': {
      const c = CONNECTION[st.chat] ?? { word: '?' };
      return { title: '', image: bigText(c.word, 'Chat', { fill: c.fill }) };
    }
    case 'obs': {
      const c = CONNECTION[st.obs] ?? { word: '?' };
      return { title: '', image: bigText(c.word, 'OBS', { fill: c.fill }) };
    }
  }
}

/** A switch key: lit when on, dim when off, "?" when unknown. */
export function switchView(o: { state: DeckState; sw?: string; deck?: DeckView }): KeyView {
  const wait = notReady(o.state, o.deck);
  if (wait) return wait;
  if (!o.sw) return { title: 'Pick a\nswitch', image: plain('…') };
  const s = o.deck!.switches.find((x) => switchKey(x.kind, x.id) === o.sw);
  if (!s) return notChosen('That switch');
  if (s.gone) return { title: 'Switch\ngone', image: plain('?') };
  if (s.on === null) return { title: '', image: bigText('?', s.label) };
  return { title: '', image: s.on ? bigText('ON', s.label, { fill: COLORS.green }) : bigText('OFF', s.label, { dim: true }) };
}
