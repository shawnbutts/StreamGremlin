// Messages between the key's settings panel and the plugin. The panel
// never calls StreamGremlin itself: only the plugin's Node process does
// (ADR-0042), and the token never reaches the panel.

/** Panel to plugin. */
export type ToPlugin =
  | { type: 'hello' }
  | { type: 'pair'; code: string; address?: string }
  | { type: 'unpair' };

/** Plugin to panel. */
export interface Status {
  type: 'status';
  state: 'ok' | 'unreachable' | 'unpaired';
  /** The name this deck was paired under. */
  name?: string;
  address?: string;
  /** Why the last thing asked didn't work, in words. */
  problem?: string;
  /** Said after pairing works. */
  notice?: string;
  buttons: { id: string; label: string }[];
  /** The counters and switches chosen for the deck in StreamGremlin (D-123). */
  counters: string[];
  switches: { key: string; label: string }[];
  /** False when StreamGremlin is too old for counters, status and switches. */
  deck: boolean;
}

/** The panel's line about the connection. */
export function statusWords(s: Status): string {
  switch (s.state) {
    case 'ok':
      return `Paired with StreamGremlin as “${s.name ?? 'this Stream Deck'}”.`;
    case 'unpaired':
      return 'Not paired yet. In StreamGremlin, open Connections → Stream Deck → Pair a Stream Deck, and type the code it shows here.';
    case 'unreachable':
      return s.name
        ? 'Paired, but StreamGremlin isn’t answering. Is it running? Keys show “Not running” until it is.'
        : 'StreamGremlin isn’t answering. Start it, then pair here.';
  }
}

/** Checks a typed code's shape before it's sent: 8 letters and digits. */
export function codeProblem(code: string): string | undefined {
  const c = code.toUpperCase().replace(/[\s-]/g, '');
  if (c.length === 0) return 'Type the code StreamGremlin shows.';
  if (!/^[A-Z0-9]{8}$/.test(c)) return 'The code is 8 letters and digits, like K7QX-M3PA.';
  return undefined;
}
