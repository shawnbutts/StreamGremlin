// The Live screen's parts in words (D-075): chat lines from chat events,
// buttons' ids, and which workflows start from a button.
import { SCHEMA, type Def } from './editor';
import type { ChatterCard, EventRecord, TwitchCard, WorkflowSummary } from './api';

export const BUTTON_PRESSED = 'buttons.button.pressed';
export const CHAT_MESSAGE = 'chat.message.received';

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
