// Checking a giveaway in the app (docs/adr/0025-giveaways.md): the same
// check as `streamgremlin giveaways verify`, so the streamer needn't take
// the engine's word for it.
import type { GiveawayDetail, GiveawayDraw, GiveawayEntry } from './api';

/** SHA-256 of the entry list: "<seq>\t<platform>:<user id>\n" per entry. */
export async function fingerprint(entries: GiveawayEntry[]): Promise<string> {
  const text = entries.map((e) => `${e.seq}\t${e.platform}:${e.user_id}\n`).join('');
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

/** What doesn't hold in a giveaway's record, or nothing. */
export async function verify(d: GiveawayDetail): Promise<string[]> {
  if (!d.fingerprint) return ['Entries are still open, so there’s no fingerprint to check yet.'];
  const bad: string[] = [];
  if ((await fingerprint(d.entries)) !== d.fingerprint) bad.push('The entry list doesn’t match the fingerprint recorded when entries closed.');
  const drawn = new Set<number>();
  for (const dr of [...d.draws].sort((a, b) => a.n - b.n)) {
    const cands = d.entries.filter((e) => !drawn.has(e.seq)).map((e) => e.seq);
    if (cands.length !== dr.candidates) bad.push(`Draw ${dr.n}: ${cands.length} entries weren’t drawn yet, not ${dr.candidates}.`);
    else if (cands[dr.pick_index] !== dr.entry_seq) bad.push(`Draw ${dr.n}: entry ${dr.entry_seq} isn’t number ${dr.pick_index + 1} of those not yet drawn.`);
    drawn.add(dr.entry_seq);
  }
  return bad;
}

/** A draw in words. */
export function howDrawn(dr: GiveawayDraw): string {
  return `Entry ${dr.entry_seq}, number ${dr.pick_index + 1} of the ${dr.candidates} not yet drawn`;
}

export function outcomeWords(dr: GiveawayDraw, now: number): string {
  switch (dr.outcome) {
    case 'won':
      return 'Won';
    case 'claimed':
      return 'Claimed';
    case 'forfeited':
      return 'Didn’t claim in time';
    case 'set_aside':
      return 'Set aside';
    default: {
      const left = dr.claim_until ? Math.max(0, Math.round((Date.parse(dr.claim_until) - now) / 1000)) : 0;
      return `Can claim: ${left} seconds left`;
    }
  }
}
