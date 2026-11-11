// The Live screen's parts in words (D-075): chat lines from chat events,
// buttons' ids, and which workflows start from a button.
import { SCHEMA, type Def } from './editor';
import type { EventRecord, WorkflowSummary } from './api';

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
