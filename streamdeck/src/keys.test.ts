import { describe, expect, it } from 'vitest';
import { counterView, keyView, statusView, switchView, titleLines } from './keys';
import type { DeckButton, DeckView } from './engine';

const brb: DeckButton = { id: 'brb', label: 'Be right back', color: 'blue', icon: '', hideLabel: false, starts: 1 };
const decode = (url: string) => decodeURIComponent(url.replace('data:image/svg+xml,', ''));
const glyph = { d: 'M0 0L100 0L100 200Z', bounds: [0, 0, 100, 200] as [number, number, number, number], unitsPerEm: 2048 };
const nerd = (name: string) => (name === 'md-microphone' ? glyph : undefined);

describe('keyView', () => {
  it('fills the key with the button’s colour, with its label in dark ink', () => {
    // A plugin can't colour Stream Deck's own title, and white fails on
    // these colours, so the label is drawn into the image.
    const v = keyView({ state: 'ok', buttonId: 'brb', buttons: [brb] });
    expect(v.title).toBe('');
    const img = decode(v.image);
    expect(img).toContain('<rect width="144" height="144" rx="16" fill="#3e8ed0"/>');
    expect(img).toMatch(/<text[^>]*fill="#111114"[^>]*>.*Be right.*back/s);
  });

  it('keeps an uncoloured button dark, with light ink', () => {
    const img = decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, color: '' }] }).image);
    expect(img).toContain('fill="#1c1c22"');
    expect(img).toMatch(/<text[^>]*fill="#ffffff"/);
  });

  it('draws the icon in the same ink, alone or above the label', () => {
    const only = decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'tabler:player-pause', hideLabel: true }] }).image);
    expect(only).toContain('stroke="#111114"');
    expect(only).not.toContain('<text');
    const both = decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'tabler:player-pause' }] }).image);
    expect(both).toContain('<path');
    expect(both).toContain('Be right');
  });

  it('draws a Nerd Fonts icon from its outline, as the app shows it', () => {
    const img = decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'nf:md-microphone', hideLabel: true }] }, nerd).image);
    expect(img).toContain('d="M0 0L100 0L100 200Z"');
    expect(img).toContain('fill="#111114" d="M');
    expect(img).toMatch(/scale\([\d.]+ -[\d.]+\)/); // fonts draw y up
  });

  it('shows the label when the icon can’t be drawn', () => {
    for (const lookup of [undefined, () => undefined]) {
      const img = decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, icon: 'nf:md-microphone', hideLabel: true }] }, lookup).image);
      expect(img).toContain('Be right');
      expect(img).not.toContain('<path');
    }
  });

  it('dims a button that starts nothing', () => {
    expect(decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, starts: 0 }] }).image)).toContain('opacity="0.4"');
  });

  it('escapes the label', () => {
    expect(decode(keyView({ state: 'ok', buttonId: 'brb', buttons: [{ ...brb, label: '<b>&"x' }] }).image)).toContain('&#60;b&#62;&#38;&#34;x');
  });

  it('never looks working when it isn’t', () => {
    expect(keyView({ state: 'unreachable', buttonId: 'brb', buttons: [brb] })).toMatchObject({ title: 'Not\nrunning' });
    expect(decode(keyView({ state: 'unreachable', buttonId: 'brb', buttons: [brb] }).image)).toContain('?');
    expect(keyView({ state: 'unpaired', buttonId: 'brb' }).title).toBe('Pair\nfirst');
    expect(keyView({ state: 'ok', buttons: [brb] }).title).toBe('Pick a\nbutton');
    expect(keyView({ state: 'ok', buttonId: 'gone', buttons: [brb] }).title).toBe('Button\ngone');
  });
});

describe('titleLines', () => {
  it('breaks long labels into up to three short lines', () => {
    expect(titleLines('Be right back')).toBe('Be right\nback');
    expect(titleLines('Start the raid train now')).toBe('Start the\nraid train\nnow');
    expect(titleLines('Supercalifragilistic')).toBe('Supercali…');
    expect(titleLines('one two three four five six seven')).toBe('one two\nthree four\nfive six…');
  });
});

const deck: DeckView = {
  status: { live: true, chat: 'connected', obs: 'disconnected', chat_locked: null },
  counters: [{ name: 'deaths', value: 7 }, { name: 'wins', value: null }, { name: 'mood', value: 'happy' }],
  switches: [
    { kind: 'workflow', id: 'wf_a', label: 'Say hello', on: true },
    { kind: 'timer', id: 'break', label: 'Take a break', on: false },
    { kind: 'chat_lock', label: 'Chat lock', on: null },
    { kind: 'workflow', id: 'wf_gone', label: 'Workflow gone', on: null, gone: true },
  ],
};

describe('counter, status and switch keys', () => {
  it('never shows a value without StreamGremlin', () => {
    expect(counterView({ state: 'unreachable', name: 'deaths', deck }).title).toBe('Not\nrunning');
    expect(statusView({ state: 'unpaired', what: 'live', deck }).title).toBe('Pair\nfirst');
    expect(switchView({ state: 'ok', sw: 'timer:break' }).title).toBe('Update\nStreamGremlin');
  });

  it('shows a counter’s value, 0 when unset, and ? when it isn’t a number', () => {
    expect(decode(counterView({ state: 'ok', name: 'deaths', change: 'take', deck }).image)).toMatch(/>−1<.*>7<.*deaths/s);
    expect(decode(counterView({ state: 'ok', name: 'wins', deck }).image)).toMatch(/>\+1<.*>0</s);
    expect(decode(counterView({ state: 'ok', name: 'mood', deck }).image)).toContain('>?<');
    expect(counterView({ state: 'ok', name: 'other', deck }).title).toBe('Choose in\nStreamGremlin');
    expect(counterView({ state: 'ok', deck }).title).toBe('Pick a\ncounter');
  });

  it('shows status as on, off or unknown', () => {
    expect(decode(statusView({ state: 'ok', what: 'live', deck }).image)).toMatch(/fill="#d9534f".*>LIVE</s);
    expect(decode(statusView({ state: 'ok', what: 'chat', deck }).image)).toMatch(/fill="#4caf50".*>On</s);
    expect(decode(statusView({ state: 'ok', what: 'obs', deck }).image)).toContain('>Lost<');
    expect(decode(statusView({ state: 'ok', what: 'lock', deck }).image)).toContain('>?<');
    expect(decode(statusView({ state: 'ok', what: 'live', deck: { ...deck, status: { ...deck.status, live: false } } }).image)).toContain('>Off<');
  });

  it('lights a switch when on, dims it when off, and never guesses', () => {
    expect(decode(switchView({ state: 'ok', sw: 'workflow:wf_a', deck }).image)).toMatch(/fill="#4caf50".*>ON<.*Say hello/s);
    expect(decode(switchView({ state: 'ok', sw: 'timer:break', deck }).image)).toMatch(/opacity="0.4".*>OFF</s);
    expect(decode(switchView({ state: 'ok', sw: 'chat_lock:', deck }).image)).toContain('>?<');
    expect(switchView({ state: 'ok', sw: 'workflow:wf_gone', deck }).title).toBe('Switch\ngone');
    expect(switchView({ state: 'ok', sw: 'timer:other', deck }).title).toBe('Choose in\nStreamGremlin');
  });
});
