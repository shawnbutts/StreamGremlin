import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import StreamDeckKeysCard from './StreamDeckKeysCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { DeckSettings, DeckView } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

/** A fake engine holding the choice, and what the deck sees from it. */
function engine(opts: { fail?: boolean } = {}) {
  let settings: DeckSettings = { counters: ['deaths'], switches: [{ kind: 'workflow', id: 'wf_gone' }], runs: [], scenes: false, countdowns: [], ads: false };
  const view = (): DeckView => ({
    status: { live: null, chat: 'connected', obs: 'off', chat_locked: false },
    counters: settings.counters.map((name) => ({ name, value: name === 'deaths' ? 7 : null })),
    switches: settings.switches.map((s) => ({ ...s, label: s.id === 'wf_gone' ? 'Workflow gone' : 'x', on: s.id === 'wf_gone' ? null : true, gone: s.id === 'wf_gone' })),
    runs: settings.runs.map((id) => ({ id, label: 'Say hello', on: true, needs_viewer: true })),
    scenes: settings.scenes ? { known: false, current: '', list: [] } : null,
    countdowns: [],
    ads: null,
  });
  const calls = mockFetch({
    '/v1/settings/streamdeck': (method, body) => {
      if (opts.fail) return { status: 503, body: { error: { code: 'unavailable', message: 'engine busy' } } };
      if (method === 'PUT') settings = body as DeckSettings;
      return { status: 200, body: settings };
    },
    '/v1/deck': () => ({ status: 200, body: view() }),
    '/v1/workflows': () => ({ status: 200, body: { workflows: [{ id: 'wf_hello', name: 'Say hello', enabled: true }] } }),
    '/v1/timers': () => ({ status: 200, body: { timers: [{ id: 'break', name: 'Take a break', enabled: false }] } }),
    '/v1/overlays': () => ({
      status: 200,
      body: { overlays: [{ name: 'brb', kind: 'timer', look: { timer_mode: 'countdown', timer_label: 'Back soon' } }, { name: 'time', kind: 'timer', look: { timer_mode: 'clock' } }] },
    }),
    '/v1/variables': () => ({ status: 200, body: { variables: [{ scope: 'global', owner: '', name: 'wins', value: 3, updated_at: '', updated_by: 'you' }] } }),
  });
  return { calls, settings: () => settings };
}

const text = () => document.body.textContent ?? '';
const box = (label: string) =>
  Array.from(document.querySelectorAll<HTMLLabelElement>('label.pick')).find((l) => l.textContent?.includes(label))?.querySelector('input') as HTMLInputElement;

describe('StreamDeckKeysCard', () => {
  it('shows the status, the counters with their values, and a deleted switch', async () => {
    engine();
    comp = mount(StreamDeckKeysCard, { target: document.body });
    await settle();
    expect(text()).toContain('Now: live unknown · chat connected · OBS off · chat lock off');
    expect(text()).toContain('deaths now 7');
    expect(text()).toContain('A chosen workflow was deleted.');
    expect(document.querySelector('datalist option')?.getAttribute('value')).toBe('wins');
    for (const theme of ['dark', 'light', 'midnight']) {
      document.documentElement.dataset.theme = theme;
      const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
      expect(r.violations.map((v) => v.id), theme).toEqual([]);
    }
  });

  it('adds and removes counters, checking the name first', async () => {
    const e = engine();
    comp = mount(StreamDeckKeysCard, { target: document.body });
    await settle();
    const input = document.querySelector<HTMLInputElement>('#deck-counter')!;
    input.value = 'Bad Name';
    input.dispatchEvent(new Event('input'));
    document.querySelector('form')!.dispatchEvent(new SubmitEvent('submit', { cancelable: true }));
    flushSync();
    expect(text()).toContain('lowercase letters');
    expect(e.calls.some((c) => c.method === 'PUT')).toBe(false);

    input.value = 'wins';
    input.dispatchEvent(new Event('input'));
    document.querySelector('form')!.dispatchEvent(new SubmitEvent('submit', { cancelable: true }));
    await settle();
    expect(e.settings().counters).toEqual(['deaths', 'wins']);
    expect(text()).toContain('wins can be a counter key now.');

    document.querySelector<HTMLButtonElement>('button[aria-label="Stop using deaths as a counter"]')!.click();
    await settle();
    expect(e.settings().counters).toEqual(['wins']);
  });

  it('ticks and unticks switches', async () => {
    const e = engine();
    comp = mount(StreamDeckKeysCard, { target: document.body });
    await settle();
    box('The chat lock').click();
    await settle();
    expect(e.settings().switches).toContainEqual({ kind: 'chat_lock' });
    box('Take a break').click();
    await settle();
    expect(e.settings().switches).toContainEqual({ kind: 'timer', id: 'break' });
    expect(text()).toContain('Take a break can be a switch key now.');
    box('The chat lock').click();
    await settle();
    expect(e.settings().switches).not.toContainEqual({ kind: 'chat_lock' });
  });

  it('chooses workflows to run, warning when they need a viewer, and scenes, countdowns and ads', async () => {
    const e = engine();
    comp = mount(StreamDeckKeysCard, { target: document.body });
    await settle();
    expect(text()).not.toContain('(clock)'); // a plain clock isn't a countdown
    (Array.from(document.querySelectorAll<HTMLLabelElement>('label.pick')).filter((l) => l.textContent?.includes('Say hello'))[1].querySelector('input') as HTMLInputElement).click();
    await settle();
    expect(e.settings().runs).toEqual(['wf_hello']);
    expect(text()).toContain('starts from a viewer or their message');
    box('Back soon').click();
    await settle();
    expect(e.settings().countdowns).toEqual(['brb']);
    box('Scene keys').click();
    await settle();
    box('An ad key').click();
    await settle();
    expect(e.settings()).toMatchObject({ scenes: true, ads: true });
  });

  it('says unknown when the engine can’t say', async () => {
    engine({ fail: true });
    comp = mount(StreamDeckKeysCard, { target: document.body });
    await settle();
    expect(document.querySelector('[role="alert"]')?.textContent).toContain('Unknown: engine busy');
  });
});
