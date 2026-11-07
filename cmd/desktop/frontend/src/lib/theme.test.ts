import { describe, expect, it } from 'vitest';
import { applyTheme, resolveTheme } from './theme';

describe('themes', () => {
  it('resolves system to the OS preference', () => {
    expect(resolveTheme('system', true)).toBe('light');
    expect(resolveTheme('system', false)).toBe('dark');
    expect(resolveTheme('midnight', true)).toBe('midnight');
  });

  it('sets data-theme on the page', () => {
    applyTheme('midnight');
    expect(document.documentElement.dataset.theme).toBe('midnight');
  });
});
