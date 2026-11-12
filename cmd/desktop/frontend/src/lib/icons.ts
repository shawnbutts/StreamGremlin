// Button icons (D-076): Tabler Icons (MIT, tabler.io/icons), bundled with
// the app so they work offline. Each icon is a few SVG elements; the pack
// is loaded the first time it's needed, not with the app.

/** One SVG element of an icon: its tag and attributes. */
export type IconNode = [string, Record<string, string>];
export type IconPack = Record<string, IconNode[]>;

let loading: Promise<IconPack> | undefined;

/** The icon pack, loaded once. */
export function loadIcons(): Promise<IconPack> {
  loading ??= import('tabler-icons-outline').then((m) => m.default as unknown as IconPack);
  return loading;
}

/** The pack's name for a saved icon (tabler:NAME), or '' for none. */
export function iconName(id: string): string {
  return id.startsWith('tabler:') ? id.slice('tabler:'.length) : '';
}

/** Icons a streamer often wants, shown before any search. */
export const SUGGESTED = [
  'player-play', 'player-pause', 'player-stop', 'microphone', 'microphone-off', 'video', 'video-off', 'camera',
  'music', 'volume', 'volume-off', 'coffee', 'moon', 'door-exit', 'bell', 'gift', 'heart', 'star', 'trophy',
  'confetti', 'sparkles', 'flame', 'bolt', 'clock', 'message', 'users', 'dice-5', 'brand-twitch', 'brand-discord',
  'brand-youtube',
];

/**
 * Icons whose name holds every word searched, closest first: the name
 * itself, then names starting with the first word, then the rest by length.
 */
export function searchIcons(pack: IconPack, query: string, limit = 60): string[] {
  const words = query.toLowerCase().split(/[\s-]+/).filter(Boolean);
  if (words.length === 0) return SUGGESTED.filter((n) => pack[n]).slice(0, limit);
  const whole = words.join('-');
  const rank = (n: string) => (n === whole ? 0 : n.startsWith(words[0]) ? 1 : 2);
  return Object.keys(pack)
    .filter((n) => words.every((w) => n.includes(w)))
    .sort((a, b) => rank(a) - rank(b) || a.length - b.length || a.localeCompare(b))
    .slice(0, limit);
}
