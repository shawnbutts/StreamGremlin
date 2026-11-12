import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadNerd } from './nerd';

describe('loadNerd', () => {
  it('reads the outlines shipped with the plugin, once', async () => {
    const file = join(import.meta.dirname, '..', 'com.streamgremlin.streamdeck.sdPlugin', 'icons', 'nerd-paths.json.gz');
    const nerd = await loadNerd(file);
    const mic = nerd('md-microphone')!;
    expect(mic.d).toMatch(/^M/);
    expect(mic.bounds).toHaveLength(4);
    expect(mic.unitsPerEm).toBe(2048);
    expect(nerd('fa-twitch')).toBeTruthy();
    expect(nerd('no-such-icon')).toBeUndefined();
    expect(await loadNerd(file)).toBe(nerd);
  });

  it('draws nothing when the file is missing', async () => {
    const nerd = await loadNerd('/nonexistent/nerd-paths.json.gz');
    expect(nerd('md-microphone')).toBeUndefined();
  });
});
