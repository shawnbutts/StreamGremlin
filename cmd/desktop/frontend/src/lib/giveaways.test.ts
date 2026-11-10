import { describe, expect, it } from 'vitest';
import { chancesWords, fingerprint, holder, howDrawn, qualifiedWords, refusalWords, verify, who } from './giveaways';
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

describe('extra chances', () => {
  const e = (seq: number, chances: number): GiveawayEntry => ({ ...entry(seq), chances });
  it('matches the engine’s weighted fingerprint format', async () => {
    // printf '1\ttwitch:u1\t3\n2\ttwitch:u2\t1\n' | shasum -a 256
    expect(await fingerprint([e(1, 3), e(2, 1)], true)).toBe('a5af751606ed5138d5914531a4283e9fce9b5391ceeb02ad250abf3b4f8faa97');
  });
  it('finds a ticket’s holder, and checks ticket draws', async () => {
    const es = [e(1, 3), e(2, 1), e(3, 2)];
    expect([0, 1, 2, 3, 4, 5, 6].map((t) => holder(es, t)?.seq)).toEqual([1, 1, 1, 2, 3, 3, undefined]);
    const d = {
      entries: es, subscriber_chances: 3, fingerprint: await fingerprint(es, true),
      draws: [
        { id: 'a', n: 1, entry_seq: 3, pick_index: 5, candidates: 6, outcome: 'won', drawn_at: '', winner: es[2] },
        { id: 'b', n: 2, entry_seq: 2, pick_index: 3, candidates: 4, outcome: 'won', drawn_at: '', winner: es[1] },
      ],
    } as unknown as GiveawayDetail;
    expect(await verify(d)).toEqual([]);
    d.draws[1] = { ...d.draws[1], pick_index: 2 };
    expect((await verify(d))[0]).toContain('doesn’t hold ticket 3');
    expect(howDrawn(d.draws[0], true)).toBe('Entry 3, ticket 6 of the 6 held by those not yet drawn');
    expect(chancesWords({ subscriber_chances: 1 })).toBe('every entry has the same chance');
  });
});
