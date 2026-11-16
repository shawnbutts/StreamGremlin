import { describe, expect, it } from 'vitest';
import { codeProblem, statusWords } from './messages';

describe('statusWords', () => {
  it('never says paired when it can’t tell', () => {
    expect(statusWords({ type: 'status', state: 'ok', name: 'Desk deck', buttons: [], counters: [], switches: [], runs: [], scenes: [], countdowns: [], deck: true })).toBe('Paired with StreamGremlin as “Desk deck”.');
    expect(statusWords({ type: 'status', state: 'unpaired', buttons: [], counters: [], switches: [], runs: [], scenes: [], countdowns: [], deck: true })).toContain('Pair a Stream Deck');
    expect(statusWords({ type: 'status', state: 'unreachable', name: 'Desk deck', buttons: [], counters: [], switches: [], runs: [], scenes: [], countdowns: [], deck: true })).toContain('isn’t answering');
    expect(statusWords({ type: 'status', state: 'unreachable', buttons: [], counters: [], switches: [], runs: [], scenes: [], countdowns: [], deck: true })).toContain('Start it, then pair');
  });
});

describe('codeProblem', () => {
  it('takes the code typed loosely, and says what’s wrong otherwise', () => {
    expect(codeProblem('k7qx-m3pa')).toBeUndefined();
    expect(codeProblem(' K7QX M3PA ')).toBeUndefined();
    expect(codeProblem('')).toContain('Type the code');
    expect(codeProblem('K7QX')).toContain('8 letters');
    expect(codeProblem('K7QX-M3P!')).toContain('8 letters');
  });
});
