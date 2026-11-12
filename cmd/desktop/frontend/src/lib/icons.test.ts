import { describe, expect, it } from 'vitest';
import { SUGGESTED, iconDef, iconName, iconSet, loadSet, searchIcons } from './icons';

describe('icons', () => {
  it('loads each bundled set once, offline', async () => {
    const tabler = await loadSet('tabler');
    expect(tabler.names.length).toBeGreaterThan(4000);
    expect(await loadSet('tabler')).toBe(tabler);
    const nerd = await loadSet('nf');
    expect(nerd.names.length).toBeGreaterThan(10000);
    expect(nerd.names).toContain('md-microphone');
    expect(nerd.names.some((n) => n.startsWith('ple-'))).toBe(false); // terminal glyphs left out
  });

  it('names icons by set, and draws them', async () => {
    expect([iconSet('tabler:player-pause'), iconName('tabler:player-pause')]).toEqual(['tabler', 'player-pause']);
    expect([iconSet('nf:md-microphone_off'), iconName('nf:md-microphone_off')]).toEqual(['nf', 'md-microphone_off']);
    expect([iconSet('player-pause'), iconName('player-pause')]).toEqual(['', '']);
    const sets = { tabler: await loadSet('tabler'), nf: await loadSet('nf') };
    expect(iconDef(sets, 'tabler:brand-twitch')).toMatchObject({ kind: 'svg' });
    const mic = iconDef(sets, 'nf:md-microphone')!;
    expect(mic.kind).toBe('glyph');
    expect(mic.kind === 'glyph' && mic.char.codePointAt(0)).toBeGreaterThan(0xe000); // a private-use character
    expect(iconDef(sets, 'nf:md-no-such')).toBeUndefined();
    expect(iconDef({}, 'nf:md-microphone')).toBeUndefined(); // not loaded yet
  });

  it('suggests icons that exist, and finds by every word, closest first', async () => {
    for (const set of ['tabler', 'nf'] as const) {
      const s = await loadSet(set);
      expect(SUGGESTED[set].every((n) => s.names.includes(n))).toBe(true);
      expect(searchIcons(s, '')).toEqual(SUGGESTED[set]);
    }
    const tabler = await loadSet('tabler');
    const found = searchIcons(tabler, 'Player  Pause');
    expect(found[0]).toBe('player-pause');
    expect(found.every((n) => n.includes('player') && n.includes('pause'))).toBe(true);
    expect(searchIcons(tabler, 'a', 10)).toHaveLength(10);
    expect(searchIcons(tabler, 'zzzz-nothing')).toEqual([]);
    const nerd = await loadSet('nf');
    expect(searchIcons(nerd, 'microphone off')).toContain('md-microphone_off');
    expect(searchIcons(nerd, 'twitch')).toContain('fa-twitch');
  });
});
