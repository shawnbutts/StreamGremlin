// Button icons (D-076, ADR-0040): two sets, bundled with the app so they
// work offline, each loaded the first time it's needed.
// - tabler: Tabler Icons (MIT), drawn as SVG.
// - nf: Nerd Fonts' symbols (Material Design, Font Awesome, Codicons and
//   more; see THIRD-PARTY-NOTICES.md), drawn from a font.
// A saved icon is SET:NAME, like tabler:player-pause or nf:md-microphone.

/** One SVG element of an icon: its tag and attributes. */
export type IconNode = [string, Record<string, string>];
export type IconPack = Record<string, IconNode[]>;
export type SetId = 'tabler' | 'nf';

/** How to draw one icon. */
export type IconDef = { kind: 'svg'; nodes: IconNode[] } | { kind: 'glyph'; char: string };

/** A loaded set: its names, and how to draw each. */
export interface IconSet {
  id: SetId;
  names: string[];
  def: (name: string) => IconDef | undefined;
}

export const SETS: { id: SetId; name: string }[] = [
  { id: 'tabler', name: 'Tabler Icons' },
  { id: 'nf', name: 'Nerd Fonts' },
];

const loading: Partial<Record<SetId, Promise<IconSet>>> = {};

/** A set, loaded once. */
export function loadSet(id: SetId): Promise<IconSet> {
  loading[id] ??=
    id === 'tabler'
      ? import('tabler-icons-outline').then((m) => {
          const pack = m.default as unknown as IconPack;
          return { id, names: Object.keys(pack), def: (n: string) => (pack[n] ? { kind: 'svg' as const, nodes: pack[n] } : undefined) };
        })
      : import('../assets/nerd/glyphs.json').then((m) => {
          const glyphs = (m.default as { glyphs: Record<string, number> }).glyphs;
          return {
            id,
            names: Object.keys(glyphs),
            def: (n: string) => (glyphs[n] ? { kind: 'glyph' as const, char: String.fromCodePoint(glyphs[n]) } : undefined),
          };
        });
  return loading[id]!;
}

/** The set a saved icon is from, or '' for none or an unknown set. */
export function iconSet(id: string): SetId | '' {
  const set = id.slice(0, id.indexOf(':'));
  return set === 'tabler' || set === 'nf' ? set : '';
}

/** A saved icon's name within its set, or ''. */
export function iconName(id: string): string {
  return iconSet(id) ? id.slice(id.indexOf(':') + 1) : '';
}

/** How to draw a saved icon, when its set is loaded and knows it. */
export function iconDef(sets: Partial<Record<SetId, IconSet>>, id: string): IconDef | undefined {
  const set = iconSet(id);
  return set ? sets[set]?.def(iconName(id)) : undefined;
}

/** Icons a streamer often wants, shown before any search. */
export const SUGGESTED: Record<SetId, string[]> = {
  tabler: [
    'player-play', 'player-pause', 'player-stop', 'microphone', 'microphone-off', 'video', 'video-off', 'camera',
    'music', 'volume', 'volume-off', 'coffee', 'moon', 'door-exit', 'bell', 'gift', 'heart', 'star', 'trophy',
    'confetti', 'sparkles', 'flame', 'bolt', 'clock', 'message', 'users', 'dice-5', 'brand-twitch', 'brand-discord',
    'brand-youtube',
  ],
  nf: [
    'md-play', 'md-pause', 'md-stop', 'md-microphone', 'md-microphone_off', 'md-video', 'md-video_off', 'md-camera',
    'md-music', 'md-volume_high', 'md-volume_off', 'md-coffee', 'md-weather_night', 'md-exit_run', 'md-bell', 'md-gift',
    'md-heart', 'md-star', 'md-trophy', 'md-party_popper', 'md-fire', 'md-lightning_bolt', 'md-clock', 'md-chat',
    'md-account_group', 'md-dice_5', 'md-gamepad_variant', 'fa-twitch', 'fa-discord', 'fa-youtube',
  ],
};

/**
 * Icons whose name holds every word searched, closest first: the name
 * itself, then names starting with the first word, then the rest by length.
 * Nerd Fonts' names start with their source set (md-, fa-), which counts as
 * part of the name.
 */
export function searchIcons(set: IconSet, query: string, limit = 60): string[] {
  const words = query.toLowerCase().split(/[\s_-]+/).filter(Boolean);
  if (words.length === 0) return SUGGESTED[set.id].filter((n) => set.def(n)).slice(0, limit);
  const bare = (n: string) => (set.id === 'nf' ? n.slice(n.indexOf('-') + 1) : n);
  const whole = words.join('-');
  const rank = (n: string) => {
    const b = bare(n).replaceAll('_', '-');
    return b === whole ? 0 : b.startsWith(words[0]) ? 1 : 2;
  };
  return set.names
    .filter((n) => words.every((w) => n.includes(w)))
    .sort((a, b) => rank(a) - rank(b) || a.length - b.length || a.localeCompare(b))
    .slice(0, limit);
}
