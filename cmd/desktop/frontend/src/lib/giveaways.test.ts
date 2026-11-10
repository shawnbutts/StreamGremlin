import { describe, expect, it } from 'vitest';
import { fingerprint, qualifiedWords, refusalWords, verify, who } from './giveaways';
import type { Giveaway, GiveawayDetail, GiveawayEntry } from './api';

const entry = (seq: number): GiveawayEntry => ({ seq, platform: 'twitch', user_id: `u${seq}`, login: `v${seq}`, display_name: `V${seq}`, entered_at: '' });

describe('giveaway checks', () => {
  it('matches the engine’s fingerprint format', async () => {
    // printf '1\ttwitch:u1\n2\ttwitch:u2\n' | shasum -a 256
    expect(await fingerprint([entry(1), entry(2)])).toBe('104215ce233df7bab9166cd87d6aa326afac91317413b47e82f80ad131340060');
  });

  it('accepts a true record and catches a changed one', async () => {
    const entries = [entry(1), entry(2), entry(3)];
    const d = {
      fingerprint: await fingerprint(entries), entries,
      draws: [
        { id: 'a', n: 1, entry_seq: 2, pick_index: 1, candidates: 3, outcome: 'won', drawn_at: '', winner: entries[1] },
        { id: 'b', n: 2, entry_seq: 3, pick_index: 1, candidates: 2, outcome: 'won', drawn_at: '', winner: entries[2] },
      ],
    } as unknown as GiveawayDetail;
    expect(await verify(d)).toEqual([]);
    expect(await verify({ ...d, entries: [entry(1), entry(2), { ...entry(3), user_id: 'other' }] })).toHaveLength(1);
    expect((await verify({ ...d, draws: [{ ...d.draws[0], pick_index: 0 }, d.draws[1]] }))[0]).toContain('Draw 1');
  });
});

describe('entry rules in words', () => {
  const g = (over: Partial<Giveaway>) => ({ subscribers_only: false, rules: {}, ...over }) as Giveaway;
  it('says who can enter, as chat hears it', () => {
    expect(who(g({}))).toBe('');
    expect(who(g({ subscribers_only: true }))).toBe('subscribers');
    expect(who(g({ subscribers_only: true, rules: { sub_months: 1, followers: true, account_days: 1, messages: 1, win_cooldown_days: 7 } }))).toBe(
      'subscribers of 1 month or more; followers; Twitch accounts at least 1 day old; after 1 message in chat this stream; no giveaway win in the last 7 days',
    );
    expect(who(g({ rules: { followers: true, follow_days: 30 } }))).toBe('followers of 30 days or more');
  });
  it('says why someone was turned away, and what an entry was checked against', () => {
    expect(refusalWords('messages', g({ rules: { messages: 3 } }))).toBe('fewer than 3 messages in chat this stream');
    expect(refusalWords('something new', g({}))).toContain('try again');
    expect(qualifiedWords(entry(1))).toBe('');
    expect(qualifiedWords({ ...entry(1), qualified: { sub_months: 2, messages: 0 } })).toBe('subscribed 2 months, 0 messages');
  });
});
