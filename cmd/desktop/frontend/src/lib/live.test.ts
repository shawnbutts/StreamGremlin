import { describe, expect, it } from 'vitest';
import { buttonId, chatLine, pressWords, recordWords, since, startsFrom, startsWords, starterWorkflow, twitchRows } from './live';
import type { ChatterCard, EventRecord, TwitchCard, WorkflowSummary } from './api';

const ev = (payload: Record<string, unknown>, extra: Partial<EventRecord> = {}): EventRecord => ({
  seq: 7,
  event_id: 'e',
  type: 'chat.message.received',
  schema_version: 1,
  source: 'twitch',
  occurred_at: '2026-11-11T20:00:00Z',
  received_at: '2026-11-11T20:00:00Z',
  actor: { platform: 'twitch', id: '42', login: 'fan', display_name: 'Fan' },
  payload,
  delivery_quality: 'confirmed',
  ...extra,
});

const wf = (id: string, name: string, where: Record<string, unknown> | undefined, running = true): WorkflowSummary => ({
  id, name, enabled: running, running, version: 1, updated_at: '', triggers: [{ type: 'buttons.button.pressed', where }],
});

describe('chatLine', () => {
  it('reads a Twitch chat message', () => {
    const l = chatLine(ev({
      message_id: 'm1', text: 'hello', color: '#FF0000',
      chatter: { id: '42', login: 'fan', display_name: 'Fan', badges: [{ set_id: 'moderator' }, { set_id: 'subscriber' }, { set_id: 'odd' }] },
    }));
    expect(l).toMatchObject({ seq: 7, messageId: 'm1', userId: '42', name: 'Fan', text: 'hello', color: '#FF0000',
      badges: ['Moderator', 'Subscriber'], test: false, shared: false });
  });
  it('marks test and shared-chat messages, and ignores odd colours', () => {
    const l = chatLine(ev({ text: 'x', color: 'red; background: url(x)', source_broadcaster_id: '9' }, { delivery_quality: 'synthetic' }));
    expect(l.test).toBe(true);
    expect(l.shared).toBe(true);
    expect(l.color).toBe('');
    expect(l.name).toBe('Fan'); // from the actor
  });
});

describe('buttons', () => {
  it('makes ids from labels', () => {
    expect(buttonId('Be right back!', [])).toBe('be-right-back');
    expect(buttonId('Be right back', ['be-right-back', 'be-right-back-2'])).toBe('be-right-back-3');
    expect(buttonId('🎉🎉', [])).toBe('button');
    expect(buttonId('x'.repeat(100), []).length).toBeLessThanOrEqual(64);
  });
  it('finds the workflows that start from a button', () => {
    const ws = [wf('a', 'A', { button_id: 'brb' }), wf('b', 'B', { button_id: ['brb', 'hype'] }), wf('c', 'C', undefined), wf('d', 'D', { button_id: 'hype' })];
    expect(startsFrom('brb', ws).map((w) => w.id)).toEqual(['a', 'b', 'c']);
    expect(startsWords('end', [])).toBe('No workflow starts from it yet.');
    expect(startsWords('brb', [wf('a', 'A', { button_id: 'brb' }), wf('b', 'B', { button_id: 'brb' }, false)])).toBe('Starts “A”; “B” is off.');
  });
  it('says what a press did', () => {
    expect(pressWords('BRB', [{ name: 'Be right back' }], false)).toBe('Pressed “BRB”: started “Be right back”.');
    expect(pressWords('BRB', [], false)).toBe('Pressed “BRB”. No workflow that’s on starts from it.');
    expect(pressWords('BRB', [], true)).toContain('nothing ran twice');
  });
  it('starts a workflow for a button', () => {
    const s = starterWorkflow({ id: 'brb', label: 'BRB' });
    expect(s.def.triggers).toEqual([{ type: 'buttons.button.pressed', where: { button_id: 'brb' } }]);
    expect(s.blanks[0].path).toBe('$.steps[0].with.message');
  });
});

describe('viewer card', () => {
  const now = new Date('2026-11-11T20:00:00Z');
  const base: TwitchCard = { user_id: '42', unknown: [], read_at: '2026-11-11T20:00:00Z' };
  it('says how long ago', () => {
    expect(since('2026-11-11T10:00:00Z', now)).toBe('today');
    expect(since('2026-11-01T10:00:00Z', now)).toBe('10 days');
    expect(since('2026-05-01T10:00:00Z', now)).toBe('6 months');
    expect(since('2019-05-06T00:00:00Z', now)).toBe('7 years');
  });
  it('puts every fact Twitch gave in words', () => {
    const rows = twitchRows(
      {
        ...base,
        created_at: '2019-05-06T00:00:00Z',
        broadcaster_type: 'affiliate',
        follow: { follows: true, followed_at: '2024-03-01T12:00:00Z' },
        subscription: { subscribed: true, tier: 1, gift: true, gifter: 'carol' },
        vip: true,
        moderator: false,
        ban: { banned: false },
      },
      now,
    );
    const by = Object.fromEntries(rows.map((r) => [r.label, r]));
    expect(by.Account.value).toContain('Twitch Affiliate');
    expect(by.Account.value).toContain('7 years ago');
    expect(by.Follows.value).toContain('2 years');
    expect(by.Subscribed.value).toBe('Tier 1, a gift from carol');
    expect(by.VIP.value).toBe('Yes');
    expect(by.Moderator.value).toBe('No');
    expect(by.Banned.value).toBe('No');
    expect(rows.some((r) => r.unknown)).toBe(false);
  });
  it('shows what Twitch didn’t say as unknown, with why, never as no', () => {
    const rows = twitchRows({ ...base, unknown: [{ fact: 'vip', why: 'Sign in to Twitch again and allow StreamGremlin to see this.' }], vip: undefined }, now);
    const vip = rows.find((r) => r.label === 'VIP')!;
    expect(vip.unknown).toBe(true);
    expect(vip.value).toContain('Sign in to Twitch again');
    // A fact missing without a reason is still unknown.
    expect(rows.find((r) => r.label === 'Follows')!.unknown).toBe(true);
  });
  it('words bans, timeouts and anonymous gifts', () => {
    const rows = twitchRows(
      { ...base, subscription: { subscribed: true, tier: 3, gift: true, anonymous: true }, ban: { banned: true, by: 'mod', reason: 'spam' } },
      now,
    );
    expect(rows.find((r) => r.label === 'Subscribed')!.value).toBe('Tier 3, an anonymous gift');
    expect(rows.find((r) => r.label === 'Banned')!.value).toBe('Banned by mod: spam');
    const t = twitchRows({ ...base, ban: { banned: true, until: '2026-11-11T20:10:00Z' } }, now);
    expect(t.find((r) => r.label === 'Banned')!.value).toMatch(/^Timed out until /);
  });
  it('says what StreamGremlin knows', () => {
    const card: ChatterCard = { platform: 'twitch', user_id: '42', twitch: base, viewer: null, currency: 'gremlins', messages: [] };
    expect(recordWords(card, now)).toEqual(['StreamGremlin has no record of them yet.']);
    const v = { id: 'v', platform: 'twitch', user_id: '42', login: 'fan', display_name: 'Fan', first_seen: '2026-11-01T12:00:00Z', last_seen: '', messages: 1, balance: 1250 };
    expect(recordWords({ ...card, viewer: v }, now)).toEqual(['First seen Nov 1, 2026 (10 days ago); 1 message in all.', '1,250 gremlins.']);
    expect(recordWords({ ...card, twitch: null }, now)[0]).toContain('A test chatter');
  });
});
