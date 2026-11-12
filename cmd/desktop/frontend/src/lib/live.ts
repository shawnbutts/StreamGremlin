// The Live screen's parts in words (D-075): chat lines from chat events,
// buttons' ids, and which workflows start from a button.
import { SCHEMA, type Def } from './editor';
import type { ChatModes, ChatterCard, EventRecord, ModAction, ModRecord, Suspicion, TwitchCard, WorkflowSummary } from './api';

export const BUTTON_PRESSED = 'buttons.button.pressed';
export const CHAT_MESSAGE = 'chat.message.received';
export const SUSPICIOUS_MESSAGE = 'moderation.suspicious_user.message';

export interface ChatLine {
  seq: number;
  messageId: string;
  userId: string;
  login: string;
  name: string;
  text: string;
  /** Badges in words: Broadcaster, Moderator, VIP, Subscriber… */
  badges: string[];
  /** Twitch's name colour, shown as a swatch, never as the text's colour. */
  color: string;
  at: string;
  /** A test event: Twitch never saw it. */
  test: boolean;
  /** From another channel in a shared chat. */
  shared: boolean;
  /** Twitch's suspicious-user flag on this message, in words, when it has one. */
  suspicious?: string;
  /** Deleted from StreamGremlin. */
  deleted?: boolean;
}

const badgeWords: Record<string, string> = {
  broadcaster: 'Broadcaster',
  moderator: 'Moderator',
  lead_moderator: 'Lead moderator',
  vip: 'VIP',
  subscriber: 'Subscriber',
  founder: 'Founder',
  staff: 'Twitch staff',
  partner: 'Verified',
  artist: 'Artist',
};

// One letter per badge on a chat line, to keep lines short; the word
// stays for screen readers and on hover. Verified is a tick, since V is VIP.
const badgeLetters: Record<string, string> = {
  Broadcaster: 'B',
  Moderator: 'M',
  'Lead moderator': 'L',
  VIP: 'V',
  Subscriber: 'S',
  Founder: 'F',
  'Twitch staff': 'T',
  Verified: '✓',
  Artist: 'A',
};

/** A badge's letter on a chat line; a badge without one stays a word. */
export function badgeLetter(word: string): string {
  return badgeLetters[word] ?? word;
}

/** A chat event as a line on the Live screen. */
export function chatLine(ev: EventRecord): ChatLine {
  const p = ev.payload ?? {};
  const chatter = (p.chatter ?? {}) as { id?: string; login?: string; display_name?: string; badges?: { set_id: string }[] };
  const badges: string[] = [];
  for (const b of chatter.badges ?? []) {
    const w = badgeWords[b.set_id];
    if (w && !badges.includes(w)) badges.push(w);
  }
  const color = typeof p.color === 'string' && /^#[0-9a-fA-F]{6}$/.test(p.color) ? p.color : '';
  return {
    seq: ev.seq,
    messageId: typeof p.message_id === 'string' ? p.message_id : '',
    userId: chatter.id ?? ev.actor?.id ?? '',
    login: chatter.login ?? ev.actor?.login ?? '',
    name: chatter.display_name || ev.actor?.display_name || chatter.login || ev.actor?.login || 'Someone',
    text: typeof p.text === 'string' ? p.text : '',
    badges,
    color,
    at: ev.occurred_at,
    test: ev.delivery_quality === 'synthetic',
    shared: typeof p.source_broadcaster_id === 'string' && p.source_broadcaster_id !== '',
  };
}

/** "Be right back!" → "be-right-back", unique among taken. */
export function buttonId(label: string, taken: string[]): string {
  const base =
    label
      .toLowerCase()
      .normalize('NFKD')
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 56)
      .replace(/-+$/, '') || 'button';
  let id = base;
  for (let n = 2; taken.includes(id); n++) id = `${base}-${n}`;
  return id;
}

/** The workflows that start from a button, on or off. */
export function startsFrom(id: string, workflows: WorkflowSummary[]): WorkflowSummary[] {
  return workflows.filter((w) =>
    w.triggers.some((t) => {
      if (t.type !== BUTTON_PRESSED) return false;
      const v = t.where?.button_id;
      return v === undefined || v === id || (Array.isArray(v) && v.includes(id));
    }),
  );
}

/** A new workflow for a button, with the message left to fill in. */
export function starterWorkflow(b: { id: string; label: string }): { def: Def; blanks: { path: string; hint: string }[] } {
  return {
    def: {
      schema: SCHEMA,
      id: '',
      name: b.label,
      triggers: [{ type: BUTTON_PRESSED, where: { button_id: b.id } }],
      steps: [{ action: 'chat.send', with: { message: '' } }],
    },
    blanks: [{ path: '$.steps[0].with.message', hint: 'what to say in chat' }],
  };
}

const quoted = (ws: { name: string }[]) => ws.map((w) => `“${w.name}”`).join(', ');

/** What a press did, in words. */
export function pressWords(label: string, started: { name: string }[], duplicate: boolean): string {
  if (duplicate) return `“${label}” was already pressed; nothing ran twice.`;
  if (started.length === 0) return `Pressed “${label}”. No workflow that’s on starts from it.`;
  return `Pressed “${label}”: started ${quoted(started)}.`;
}

/** What a button starts, in words, from every workflow (on or off). */
export function startsWords(id: string, workflows: WorkflowSummary[]): string {
  const all = startsFrom(id, workflows);
  if (all.length === 0) return 'No workflow starts from it yet.';
  const on = all.filter((w) => w.running ?? w.enabled);
  const off = all.filter((w) => !(w.running ?? w.enabled));
  const parts: string[] = [];
  if (on.length) parts.push(`Starts ${quoted(on)}`);
  if (off.length) parts.push(`${quoted(off)} ${off.length === 1 ? 'is' : 'are'} off`);
  return parts.join('; ') + '.';
}

// The viewer card (D-078).

export interface CardRow {
  label: string;
  value: string;
  /** Twitch didn't say: value is why. */
  unknown?: boolean;
}

const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });

/** "3 years", "5 months", "12 days", "today": how long ago, roughly. */
export function since(iso: string, now = new Date()): string {
  const days = Math.floor((now.getTime() - new Date(iso).getTime()) / 86_400_000);
  const n = (k: number, one: string) => `${k} ${one}${k === 1 ? '' : 's'}`;
  if (days < 1) return 'today';
  if (days < 60) return n(days, 'day');
  if (days < 730) return n(Math.floor(days / 30.44), 'month');
  return n(Math.floor(days / 365.25), 'year');
}

const staffWords: Record<string, string> = { staff: 'Twitch staff', admin: 'Twitch admin', global_mod: 'Twitch global moderator' };

/** What Twitch says about a chatter, a row per fact, unknowns included. */
export function twitchRows(c: TwitchCard, now = new Date()): CardRow[] {
  const why = (fact: string) => c.unknown.find((u) => u.fact === fact)?.why ?? 'Twitch didn’t say.';
  const row = (label: string, fact: string, known: boolean, value: () => string): CardRow =>
    known ? { label, value: value() } : { label, value: why(fact), unknown: true };
  const rows: CardRow[] = [];
  const accountKnown = !c.unknown.some((u) => u.fact === 'account');
  rows.push(
    row('Account', 'account', accountKnown, () => {
      const parts: string[] = [];
      if (c.created_at) parts.push(`made ${day(c.created_at)} (${since(c.created_at, now)} ago)`);
      if (c.broadcaster_type === 'partner') parts.push('Twitch Partner');
      if (c.broadcaster_type === 'affiliate') parts.push('Twitch Affiliate');
      if (c.staff_type && staffWords[c.staff_type]) parts.push(staffWords[c.staff_type]);
      const w = parts.join(', ');
      return w ? w[0].toUpperCase() + w.slice(1) : 'Known to Twitch';
    }),
  );
  rows.push(
    row('Follows', 'follow', !!c.follow, () =>
      c.follow!.follows ? (c.follow!.followed_at ? `Since ${day(c.follow!.followed_at)} (${since(c.follow!.followed_at, now)})` : 'Yes') : 'No',
    ),
  );
  rows.push(
    row('Subscribed', 'subscription', !!c.subscription, () => {
      const s = c.subscription!;
      if (!s.subscribed) return 'No';
      let w = s.tier ? `Tier ${s.tier}` : 'Yes';
      if (s.gift) w += s.anonymous ? ', an anonymous gift' : s.gifter ? `, a gift from ${s.gifter}` : ', a gift';
      return w;
    }),
  );
  rows.push(row('VIP', 'vip', c.vip !== undefined, () => (c.vip ? 'Yes' : 'No')));
  rows.push(row('Moderator', 'moderator', c.moderator !== undefined, () => (c.moderator ? 'Yes' : 'No')));
  rows.push(
    row('Banned', 'banned', !!c.ban, () => {
      const b = c.ban!;
      if (!b.banned) return 'No';
      let w = b.until ? `Timed out until ${eventTimeOf(b.until)}` : 'Banned';
      if (b.by) w += ` by ${b.by}`;
      if (b.reason) w += `: ${b.reason}`;
      return w;
    }),
  );
  return rows;
}

const eventTimeOf = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

/** What StreamGremlin knows about a chatter, in words. */
export function recordWords(card: ChatterCard, now = new Date()): string[] {
  if (card.twitch === null) return ['A test chatter: Twitch never saw them, and StreamGremlin keeps no record of test events.'];
  const v = card.viewer;
  if (!v) return ['StreamGremlin has no record of them yet.'];
  const out = [`First seen ${day(v.first_seen)} (${since(v.first_seen, now)} ago); ${v.messages} ${v.messages === 1 ? 'message' : 'messages'} in all.`];
  out.push(`${v.balance.toLocaleString()} ${card.currency || 'points'}.`);
  return out;
}

// Moderating (D-079).

/** "A new request id", for sending a moderating request once. */
export const newRequestId = () =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`;

/** "10 minutes", "1 hour", "2 weeks": a timeout's length. */
export function lengthWords(seconds: number): string {
  const units: [number, string][] = [[604800, 'week'], [86400, 'day'], [3600, 'hour'], [60, 'minute'], [1, 'second']];
  for (const [n, w] of units) {
    if (seconds >= n && seconds % n === 0) {
      const k = seconds / n;
      return `${k} ${w}${k === 1 ? '' : 's'}`;
    }
  }
  return `${seconds} seconds`;
}

/** The timeouts offered, shortest first. */
export const TIMEOUTS = [60, 600, 3600, 86400, 604800];

/** The question asked before an action. */
export type ConfirmWords = { title: string; message: string; confirmLabel: string };

const quote = (t: string) => `“${t.length > 80 ? `${t.slice(0, 79)}…` : t}”`;

/**
 * The question to ask before a moderating action that can't be taken back,
 * or would do harm before it could be: deleting, timing out, banning,
 * warning, a shoutout, and making a moderator. Undefined for the rest,
 * which are easy to undo, so a mis-click costs nothing.
 */
export function confirmWords(
  action: ModAction,
  name: string,
  o: { seconds?: number; reason?: string; text?: string } = {},
): ConfirmWords | undefined {
  const why = o.reason?.trim() ? ` Reason: ${quote(o.reason.trim())}.` : '';
  switch (action) {
    case 'delete':
      return { title: `Delete ${name}’s message?`, confirmLabel: 'Delete',
        message: `${o.text ? `${quote(o.text)} is` : 'It’s'} removed from Twitch chat for everyone. It can’t be put back.` };
    case 'timeout': {
      const len = lengthWords(o.seconds ?? 0);
      return { title: `Time out ${name} for ${len}?`, confirmLabel: 'Time out',
        message: `${name} can’t chat for ${len}, and Twitch chat shows it. Unban on their card lifts it early.${why}` };
    }
    case 'ban':
      return { title: `Ban ${name}?`, confirmLabel: 'Ban', message: `${name} can’t chat until you unban them.${why}` };
    case 'warn':
      return { title: `Warn ${name}?`, confirmLabel: 'Warn',
        message: `Twitch shows ${name} the warning, and they can’t chat until they acknowledge it. It can’t be taken back.${why}` };
    case 'shoutout':
      return { title: `Shout out ${name}?`, confirmLabel: 'Shout out',
        message: `Twitch posts a shoutout to ${name} in your chat. It can’t be taken back, and Twitch allows only one every 2 minutes.` };
    case 'mod':
      return { title: `Make ${name} a moderator?`, confirmLabel: 'Make moderator',
        message: `${name} can then delete messages, time out and ban in your chat. Remove moderator takes it away again.` };
  }
  return undefined;
}

const doneWords: Record<string, (name: string, r: ModRecord) => string> = {
  delete: () => 'Deleted the message.',
  timeout: (n, r) => `Timed out ${n} for ${lengthWords(r.seconds ?? 0)}.`,
  ban: (n) => `Banned ${n}.`,
  unban: (n) => `Unbanned ${n}.`,
  warn: (n) => `Warned ${n}.`,
  shoutout: (n) => `Shouted out ${n}.`,
  vip: (n) => `${n} is a VIP.`,
  unvip: (n) => `${n} isn’t a VIP now.`,
  mod: (n) => `${n} is a moderator.`,
  unmod: (n) => `${n} isn’t a moderator now.`,
  monitor: (n) => `Twitch is monitoring ${n}.`,
  restrict: (n) => `Twitch restricts ${n}: only you and your mods see their messages.`,
  unsuspect: (n) => `Twitch treats ${n} as usual now.`,
  chat_modes: () => 'Chat modes changed.',
};

const actionNames: Record<string, string> = {
  delete: 'Delete a message', timeout: 'Time out', ban: 'Ban', unban: 'Unban', warn: 'Warn', shoutout: 'Shoutout',
  vip: 'Make VIP', unvip: 'Remove VIP', mod: 'Make moderator', unmod: 'Remove moderator', monitor: 'Monitor',
  restrict: 'Restrict', unsuspect: 'Stop treating as suspicious', chat_modes: 'Chat modes',
};

/** How a moderating request went, in words. */
export function modWords(r: ModRecord, name = r.login || 'them', duplicate = false): string {
  const again = duplicate ? ' (already sent; not sent again)' : '';
  if (r.state === 'done') return doneWords[r.action](name, r) + again;
  if (r.state === 'pending') return `${actionNames[r.action] ?? r.action}: still waiting for Twitch.`;
  if (r.state === 'unknown') return `Unknown: ${r.answer ?? 'Twitch didn’t answer.'}`;
  return `Not done: ${r.answer ?? 'Twitch refused.'}`;
}

/** One line of a viewer's moderation history. */
export function historyWords(r: ModRecord): string {
  let w = actionNames[r.action] ?? r.action;
  if (r.action === 'timeout' && r.seconds) w += ` ${lengthWords(r.seconds)}`;
  if (r.reason) w += ` (“${r.reason}”)`;
  const state = r.state === 'done' ? '' : r.state === 'refused' ? ': refused' : r.state === 'unknown' ? ': unknown' : ': waiting';
  return w + state;
}

const treatmentWords: Record<string, string> = {
  active_monitoring: 'monitored',
  restricted: 'restricted (only you and your mods see their messages)',
  none: 'not restricted or monitored',
};

/** Twitch's suspicious-user flags in words, from an event's payload. */
export function suspicionWords(s: { treatment?: string; low_trust_status?: string; ban_evasion?: string; types?: string[]; shared_bans?: number; shared_ban_channel_ids?: string[] }): string {
  const parts: string[] = [];
  if (s.ban_evasion === 'likely') parts.push('likely ban evader');
  else if (s.ban_evasion === 'possible') parts.push('possible ban evader');
  const shared = s.shared_bans ?? s.shared_ban_channel_ids?.length ?? 0;
  if (shared > 0 || s.types?.includes('banned_in_shared_channel')) {
    parts.push(shared > 0 ? `banned in ${shared} channel${shared === 1 ? '' : 's'} you share bans with` : 'banned in a channel you share bans with');
  }
  if (s.types?.includes('manually_added')) parts.push('marked by you or a mod');
  const t = s.treatment ?? s.low_trust_status ?? '';
  if (treatmentWords[t] && t !== 'none') parts.push(treatmentWords[t]);
  if (parts.length === 0) return t === 'none' ? 'Flagged by Twitch; not restricted or monitored' : 'Flagged by Twitch';
  return parts[0][0].toUpperCase() + parts.join(', ').slice(1);
}

/** The card's suspicious row: unknown before Twitch flags them, never "no". */
export function suspicionRow(s: Suspicion | null): CardRow {
  if (!s) return { label: 'Suspicious', value: 'Twitch hasn’t flagged them while StreamGremlin was listening.' };
  return { label: 'Suspicious', value: suspicionWords(s) };
}

/** A suspicious-user event as a chat line, for a message chat didn't carry. */
export function suspiciousLine(ev: EventRecord): ChatLine {
  const p = ev.payload ?? {};
  return {
    seq: ev.seq,
    messageId: typeof p.message_id === 'string' ? p.message_id : '',
    userId: ev.actor?.id ?? '',
    login: ev.actor?.login ?? '',
    name: ev.actor?.display_name || ev.actor?.login || 'Someone',
    text: typeof p.text === 'string' ? p.text : '',
    badges: [],
    color: '',
    at: ev.occurred_at,
    test: ev.delivery_quality === 'synthetic',
    shared: false,
    suspicious: suspicionWords(p as Parameters<typeof suspicionWords>[0]),
  };
}

/** The chat modes that are on, in words. */
export function modesWords(m: ChatModes): string {
  const on: string[] = [];
  if (m.emote_mode) on.push('emote-only');
  if (m.follower_mode) on.push(m.follower_mode_duration ? `followers-only (${lengthWords(m.follower_mode_duration * 60)})` : 'followers-only');
  if (m.slow_mode) on.push(`slow (${m.slow_mode_wait_time ?? 30} seconds)`);
  if (m.subscriber_mode) on.push('subscribers-only');
  if (m.unique_chat_mode) on.push('unique chat');
  return on.length ? `On: ${on.join(', ')}.` : 'No chat modes on.';
}
