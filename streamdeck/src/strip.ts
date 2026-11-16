// A picture of eight keys for the website (D-124), drawn by the plugin's
// own code with made-up data, so it shows exactly what a Stream Deck
// shows. `make screenshots` writes it to website/screenshots.
import type { DeckButton, DeckView } from './engine';
import { adView, countdownView, counterView, keyView, runView, sceneView, statusView, switchView, type KeyView } from './keys';

const KEY = 144;
const GAP = 28;
const COLS = 4;
const ROWS = 2;
export const WIDTH = COLS * KEY + (COLS + 1) * GAP;
export const HEIGHT = ROWS * KEY + (ROWS + 1) * GAP;

/** The strip as one SVG, keys left to right, top to bottom. */
export function strip(): string {
  const at = Date.parse('2026-11-16T20:00:00Z');
  const buttons: DeckButton[] = [{ id: 'brb', label: 'Be right back', color: 'blue', icon: 'tabler:coffee', hideLabel: false, starts: 1 }];
  const deck: DeckView = {
    status: { live: true, chat: 'connected', obs: 'connected', chat_locked: false },
    counters: [{ name: 'deaths', value: 7 }],
    switches: [{ kind: 'timer', id: 'hydrate', label: 'Hydrate', on: true }],
    runs: [{ id: 'clip', label: 'Clip that', on: true }],
    scenes: { known: true, current: 'Game', list: ['Game', 'Just chatting'] },
    countdowns: [{ name: 'brb', label: 'Back soon', mode: 'countdown', running: true, ms: 299_000, at: new Date(at).toISOString() }],
    ads: { known: true, next_ad_at: new Date(at + 270_000).toISOString(), snoozes: 3, read_at: new Date(at).toISOString() },
  };
  const ok = 'ok' as const;
  const keys: KeyView[] = [
    keyView({ state: ok, buttonId: 'brb', buttons }),
    counterView({ state: ok, name: 'deaths', change: 'add', deck }),
    statusView({ state: ok, what: 'live', deck }),
    switchView({ state: ok, sw: 'timer:hydrate', deck }),
    runView({ state: ok, id: 'clip', deck }),
    sceneView({ state: ok, scene: 'Game', deck }),
    countdownView({ state: ok, name: 'brb', deck, now: at }),
    adView({ state: ok, deck, now: at }),
  ];
  const inner = keys.map((k, i) => {
    const x = GAP + (i % COLS) * (KEY + GAP);
    const y = GAP + Math.floor(i / COLS) * (KEY + GAP);
    const svg = decodeURIComponent(k.image.replace('data:image/svg+xml,', ''));
    return svg.replace('<svg xmlns="http://www.w3.org/2000/svg" ', `<svg x="${x}" y="${y}" `);
  });
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}" role="img">` +
    `<rect width="${WIDTH}" height="${HEIGHT}" rx="28" fill="#0b0b0e"/>` +
    inner.join('') +
    `</svg>\n`
  );
}
