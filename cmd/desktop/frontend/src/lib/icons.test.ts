import { describe, expect, it } from 'vitest';
import { SUGGESTED, iconName, loadIcons, searchIcons } from './icons';

describe('icons', () => {
  it('loads the bundled pack, offline', async () => {
    const pack = await loadIcons();
    expect(Object.keys(pack).length).toBeGreaterThan(4000);
    expect(pack['brand-twitch'][0][0]).toBe('path');
    expect(await loadIcons()).toBe(pack); // loaded once
  });

  it('names icons as tabler:NAME', () => {
    expect(iconName('tabler:player-pause')).toBe('player-pause');
    expect(iconName('player-pause')).toBe('');
    expect(iconName('')).toBe('');
  });

  it('suggests icons that exist, and finds by every word, closest first', async () => {
    const pack = await loadIcons();
    expect(SUGGESTED.every((n) => pack[n])).toBe(true);
    expect(searchIcons(pack, '')).toEqual(SUGGESTED);
    const found = searchIcons(pack, 'Player  Pause');
    expect(found[0]).toBe('player-pause');
    expect(found.every((n) => n.includes('player') && n.includes('pause'))).toBe(true);
    expect(searchIcons(pack, 'twitch')).toContain('brand-twitch');
    expect(searchIcons(pack, 'a', 10)).toHaveLength(10);
    expect(searchIcons(pack, 'zzzz-nothing')).toEqual([]);
  });
});
