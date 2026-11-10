import { describe, expect, it } from 'vitest';
import type { WheelSpin } from './api';
import { percents, percentWords, verifySpin, winnerOf } from './wheels';

const choices = [
  { label: 'Mario', weight: 2 },
  { label: 'Zelda', weight: 1 },
  { label: 'Tetris', weight: 1 },
];

describe('wheel checks', () => {
  it('maps every number to its choice, as the engine does', () => {
    expect([0, 1, 2, 3, 4, -1].map((d) => winnerOf(choices, d))).toEqual([0, 0, 1, 2, -1, -1]);
    expect(percents(choices).map(percentWords)).toEqual(['50%', '25%', '25%']);
    expect(percentWords(100 / 3)).toBe('33.3%');
  });

  it('checks a spin, and says plainly when it doesn’t add up', () => {
    const sp = { choices, total: 4, draw: 2, winner: 1, label: 'Zelda', wheel_version: 1 } as WheelSpin;
    expect(verifySpin(sp)).toEqual([]);
    expect(verifySpin({ ...sp, label: 'Mario' })[0]).toContain('lands on Zelda, not Mario');
    expect(verifySpin({ ...sp, total: 5 })[0]).toContain('add up to 4');
    expect(verifySpin({ ...sp, draw: 9 })[0]).toContain('isn’t below 4');
  });
});
