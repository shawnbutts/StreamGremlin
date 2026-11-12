import { describe, expect, it } from 'vitest';
import { keyView } from './keys';
import type { DeckButton } from './engine';

const brb: DeckButton = { id: 'brb', label: 'Be right back', color: 'blue', icon: '', hideLabel: false, starts: 1 };
const decode = (url: string) => decodeURIComponent(url.replace('data:image/svg+xml,', ''));

describe('keyView', () => {
  it('shows the button: its label, colour and icon', () => {
    const v = keyView({ state: 'ok', buttonId: 'brb', buttons: [brb] });
    expect(v.title).toBe('Be right\nback');
    expect(decode(v.image)).toContain('#3e8ed0');
    const icon = keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'tabler:player-pause', hideLabel: true }] });
    expect(icon.title).toBe('');
    expect(decode(icon.image)).toContain('<path');
  });

  it('dims a button that starts nothing', () => {
    expect(decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, starts: 0 }] }).image)).toContain('opacity="0.4"');
  });

  it('draws a Nerd Fonts icon from its outline, as the app shows it', () => {
    const glyph = { d: 'M0 0L100 0L100 200Z', bounds: [0, 0, 100, 200] as [number, number, number, number], unitsPerEm: 2048 };
    const nerd = (name: string) => (name === 'md-microphone' ? glyph : undefined);
    const v = keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'nf:md-microphone', hideLabel: true }] }, nerd);
    expect(v.title).toBe('');
    const img = decode(v.image);
    expect(img).toContain('d="M0 0L100 0L100 200Z"');
    expect(img).toContain('fill="#ffffff"');
    expect(img).toMatch(/scale\([\d.]+ -[\d.]+\)/); // fonts draw y up
  });

  it('shows the label when the icon can’t be drawn', () => {
    // Not loaded yet, or a name the outlines don't have.
    for (const nerd of [undefined, () => undefined]) {
      const v = keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'nf:md-microphone', hideLabel: true }] }, nerd);
      expect(v.title).toBe('Be right\nback');
      expect(decode(v.image)).not.toContain('<path');
    }
  });

  it('never looks working when it isn’t', () => {
    expect(keyView({ state: 'unreachable', buttonId: 'brb', buttons: [brb] })).toMatchObject({ title: 'Not\nrunning' });
    expect(decode(keyView({ state: 'unreachable', buttonId: 'brb', buttons: [brb] }).image)).toContain('?');
    expect(keyView({ state: 'unpaired', buttonId: 'brb' }).title).toBe('Pair\nfirst');
    expect(keyView({ state: 'ok', buttons: [brb] }).title).toBe('Pick a\nbutton');
    expect(keyView({ state: 'ok', buttonId: 'gone', buttons: [brb] }).title).toBe('Button\ngone');
  });

  it('breaks long labels into short lines', () => {
    expect(keyView({ state: 'ok', buttonId: 'x', buttons: [{ ...brb, id: 'x', label: 'Start the raid train now' }] }).title).toBe('Start the\nraid train\nnow');
    expect(keyView({ state: 'ok', buttonId: 'x', buttons: [{ ...brb, id: 'x', label: 'Supercalifragilistic' }] }).title).toBe('Supercali…');
  });
});
