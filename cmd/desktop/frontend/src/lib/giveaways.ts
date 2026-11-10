// Checking a giveaway in the app (docs/adr/0025-giveaways.md): the same
// check as `streamgremlin giveaways verify`, so the streamer needn't take
// the engine's word for it.
import type { Giveaway, GiveawayDetail, GiveawayDraw, GiveawayEntry } from './api';

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

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
const days = (n: number) => count(n, 'day', 'days');

// Who can enter, as the giveaway's opening message says it ('' for anyone).
export function who(g: Giveaway): string {
  const r = g.rules ?? {};
  const parts: string[] = [];
  if (r.sub_months) parts.push(`subscribers of ${count(r.sub_months, 'month', 'months')} or more`);
  else if (g.subscribers_only) parts.push('subscribers');
  if (r.follow_days) parts.push(`followers of ${days(r.follow_days)} or more`);
  else if (r.followers) parts.push('followers');
  if (r.account_days) parts.push(`Twitch accounts at least ${days(r.account_days)} old`);
  if (r.messages) parts.push(`after ${count(r.messages, 'message', 'messages')} in chat this stream`);
  if (r.win_cooldown_days) parts.push(`no giveaway win in the last ${days(r.win_cooldown_days)}`);
  return parts.join('; ');
}

// Why someone was turned away, for the streamer.
export function refusalWords(reason: string, g: Giveaway): string {
  const r = g.rules ?? {};
  switch (reason) {
    case 'subscribers_only':
      return 'not a subscriber';
    case 'sub_months':
      return `subscribed less than ${count(r.sub_months ?? 0, 'month', 'months')}`;
    case 'followers':
      return 'not following';
    case 'follow_days':
      return `following less than ${days(r.follow_days ?? 0)}`;
    case 'account_days':
      return `account newer than ${days(r.account_days ?? 0)}`;
    case 'messages':
      return `fewer than ${count(r.messages ?? 0, 'message', 'messages')} in chat this stream`;
    case 'recent_win':
      return `won in the last ${days(r.win_cooldown_days ?? 0)}`;
    case 'twitch_only':
      return 'not in Twitch chat';
    default:
      return 'Twitch couldn’t be asked; they can try again';
  }
}

// What an entry was checked against.
export function qualifiedWords(e: GiveawayEntry): string {
  const q = e.qualified;
  if (!q) return '';
  const date = (iso: string) => new Date(iso).toLocaleDateString();
  const parts: string[] = [];
  if (q.followed_at) parts.push(`following since ${date(q.followed_at)}`);
  if (q.account_created_at) parts.push(`account made ${date(q.account_created_at)}`);
  if (q.sub_months) parts.push(`subscribed ${count(q.sub_months, 'month', 'months')}`);
  if (q.messages !== undefined) parts.push(count(q.messages, 'message', 'messages'));
  return parts.join(', ');
}
