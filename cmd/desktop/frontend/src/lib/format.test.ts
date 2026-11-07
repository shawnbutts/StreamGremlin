import { describe, expect, it } from 'vitest';
import { duration } from './format';

describe('duration', () => {
  it('is short and readable', () => {
    expect(duration(0)).toBe('0s');
    expect(duration(59.9)).toBe('59s');
    expect(duration(252)).toBe('4m 12s');
    expect(duration(7500)).toBe('2h 5m');
    expect(duration(3 * 86400 + 3600)).toBe('3d 1h');
    expect(duration(-5)).toBe('0s');
  });
});
