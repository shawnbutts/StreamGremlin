import { describe, expect, it } from 'vitest';
import { fingerprint, verify } from './giveaways';
import type { GiveawayDetail, GiveawayEntry } from './api';

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
