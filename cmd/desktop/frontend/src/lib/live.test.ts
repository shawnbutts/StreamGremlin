import { describe, expect, it } from 'vitest';
import { buttonId, chatLine, historyWords, lengthWords, modWords, modesWords, pressWords, recordWords, since, startsFrom, startsWords, starterWorkflow, suspicionRow, suspicionWords, suspiciousLine, twitchRows, badgeLetter, confirmWords } from './live';
import type { ChatterCard, EventRecord, ModRecord, TwitchCard, WorkflowSummary } from './api';

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

describe('confirmWords', () => {
  it('asks first for what can’t be taken back, and not for what can', () => {
    const asks = (['delete', 'timeout', 'ban', 'warn', 'shoutout', 'mod'] as const).map((a) => confirmWords(a, 'Fan', { seconds: 600, reason: 'spam', text: 'buy followers' }));
    for (const w of asks) {
      expect(w).toBeDefined();
      expect(w!.title).toMatch(/\?$/);
    }
    expect(asks[0]).toMatchObject({ title: 'Delete Fan’s message?', confirmLabel: 'Delete' });
    expect(asks[0]!.message).toContain('“buy followers”');
    expect(asks[0]!.message).toContain('can’t be put back');
    expect(asks[1]).toMatchObject({ title: 'Time out Fan for 10 minutes?', confirmLabel: 'Time out' });
    expect(asks[1]!.message).toContain('Reason: “spam”');
    expect(asks[2]).toMatchObject({ title: 'Ban Fan?', confirmLabel: 'Ban' });
    expect(asks[3]).toMatchObject({ title: 'Warn Fan?', confirmLabel: 'Warn' });
    expect(asks[3]!.message).toContain('can’t be taken back');
    expect(asks[4]).toMatchObject({ title: 'Shout out Fan?', confirmLabel: 'Shout out' });
    expect(asks[5]).toMatchObject({ title: 'Make Fan a moderator?', confirmLabel: 'Make moderator' });
    for (const a of ['unban', 'vip', 'unvip', 'unmod', 'monitor', 'restrict', 'unsuspect'] as const) expect(confirmWords(a, 'Fan')).toBeUndefined();
    // A long message is shortened in the question.
    expect(confirmWords('delete', 'Fan', { text: 'x'.repeat(300) })!.message.length).toBeLessThan(200);
  });
});

describe('badgeLetter', () => {
  it('gives each badge its own letter', () => {
    const words = ['Broadcaster', 'Moderator', 'Lead moderator', 'VIP', 'Subscriber', 'Founder', 'Twitch staff', 'Verified', 'Artist'];
    const letters = words.map(badgeLetter);
    expect(letters).toEqual(['B', 'M', 'L', 'V', 'S', 'F', 'T', '✓', 'A']);
    expect(new Set(letters).size).toBe(words.length); // never two alike
    expect(badgeLetter('Something new')).toBe('Something new'); // unknown stays a word
  });
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
    const card: ChatterCard = { platform: 'twitch', user_id: '42', twitch: base, viewer: null, currency: 'gremlins', messages: [], moderation: [], suspicious: null };
    expect(recordWords(card, now)).toEqual(['StreamGremlin has no record of them yet.']);
    const v = { id: 'v', platform: 'twitch', user_id: '42', login: 'fan', display_name: 'Fan', first_seen: '2026-11-01T12:00:00Z', last_seen: '', messages: 1, balance: 1250 };
    expect(recordWords({ ...card, viewer: v }, now)).toEqual(['First seen Nov 1, 2026 (10 days ago); 1 message in all.', '1,250 gremlins.']);
    expect(recordWords({ ...card, twitch: null }, now)[0]).toContain('A test chatter');
  });
});

describe('moderating', () => {
  const rec = (x: Partial<ModRecord>): ModRecord => ({ id: 'm', request_id: 'r', action: 'timeout', platform: 'twitch', from: 'live', state: 'done', created_at: '', ...x });
  it('says timeouts’ lengths', () => {
    expect(lengthWords(600)).toBe('10 minutes');
    expect(lengthWords(3600)).toBe('1 hour');
    expect(lengthWords(1209600)).toBe('2 weeks');
    expect(lengthWords(90)).toBe('90 seconds');
  });
  it('says how a request went, never done when it wasn’t', () => {
    expect(modWords(rec({ seconds: 600 }), 'Fan')).toBe('Timed out Fan for 10 minutes.');
    expect(modWords(rec({ action: 'ban' }), 'Fan', true)).toBe('Banned Fan. (already sent; not sent again)');
    expect(modWords(rec({ state: 'refused', answer: 'Twitch refused: already banned (400)' }))).toBe('Not done: Twitch refused: already banned (400)');
    expect(modWords(rec({ state: 'unknown', answer: 'Twitch didn’t answer' }))).toMatch(/^Unknown: /);
    expect(historyWords(rec({ seconds: 60, reason: 'calm' }))).toBe('Time out 1 minute (“calm”)');
    expect(historyWords(rec({ action: 'vip', state: 'refused' }))).toBe('Make VIP: refused');
  });
  it('puts Twitch’s suspicious flags in words', () => {
    expect(suspicionWords({ low_trust_status: 'active_monitoring', ban_evasion: 'likely', types: ['ban_evader'], shared_ban_channel_ids: [] }))
      .toBe('Likely ban evader, monitored');
    expect(suspicionWords({ treatment: 'restricted', types: ['banned_in_shared_channel'], shared_bans: 2 }))
      .toBe('Banned in 2 channels you share bans with, restricted (only you and your mods see their messages)');
    expect(suspicionWords({ treatment: 'none', types: ['manually_added'] })).toBe('Marked by you or a mod');
    expect(suspicionRow(null).value).toContain('hasn’t flagged them while StreamGremlin was listening');
  });
  it('makes a line from a suspicious message chat didn’t carry', () => {
    const l = suspiciousLine(ev({ message_id: 'm9', text: 'hi', low_trust_status: 'restricted', ban_evasion: 'possible', types: ['ban_evader'] },
      { type: 'moderation.suspicious_user.message' }));
    expect(l).toMatchObject({ messageId: 'm9', text: 'hi', name: 'Fan', userId: '42' });
    expect(l.suspicious).toContain('Possible ban evader');
  });
  it('says the chat modes', () => {
    const off = { emote_mode: false, follower_mode: false, follower_mode_duration: null, slow_mode: false, slow_mode_wait_time: null, subscriber_mode: false, unique_chat_mode: false };
    expect(modesWords(off)).toBe('No chat modes on.');
    expect(modesWords({ ...off, slow_mode: true, slow_mode_wait_time: 10, follower_mode: true, follower_mode_duration: 60 }))
      .toBe('On: followers-only (1 hour), slow (10 seconds).');
  });
});
