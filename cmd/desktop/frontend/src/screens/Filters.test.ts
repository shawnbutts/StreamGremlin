import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Filters from './Filters.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { BlockedTerm, FilterCatch, FilterSettings } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Filters, { target, props: { refreshMs: 60000 } });
  return target;
}

const rule = { on: false, action: 'delete' as const, seconds: 0, exempt_vips: true, exempt_subscribers: false };
const defaults = (): FilterSettings => ({
  on: false,
  words: { ...rule, list: [] },
  links: { ...rule, allowed: ['twitch.tv', 'clips.twitch.tv'] },
  emotes: { ...rule, max: 10 },
  caps: { ...rule, min_letters: 10, percent: 70 },
  symbols: { ...rule, min_length: 15, percent: 50 },
  repeats: { ...rule, times: 3 },
});

const caught: FilterCatch[] = [
  { at: new Date().toISOString(), rule: 'links', action: 'delete', state: 'done', text: 'cheap followers at bit.ly/x', user_id: '1', login: 'spammer', name: 'Spammer' },
  { at: new Date().toISOString(), rule: 'caps', action: 'timeout', seconds: 600, state: 'refused', answer: 'Sign in to Twitch again and allow StreamGremlin to moderate.', text: 'HELLO', user_id: '2', login: 'loud', name: 'Loud' },
];

const label = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.trim().startsWith(text))!;
const input = (el: ParentNode, text: string) => label(el, text).querySelector('input, select, textarea') as HTMLInputElement;
const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text)!;
const typed = (i: HTMLInputElement | HTMLTextAreaElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

function engine(recent: FilterCatch[] = [], terms: BlockedTerm[] = []) {
  return mockFetch({
    '/v1/filters': (m, body) => ({ status: 200, body: { settings: m === 'PUT' ? body : defaults(), recent } }),
    '/v1/filters/blocked-terms': (m, body) => {
      if (m === 'GET') return { status: 200, body: { terms } };
      const texts = (body as { texts: string[] }).texts;
      const results = texts.map((text) =>
        text.length < 2
          ? { text, state: 'refused', answer: 'The length of the term in the text field is either too short or too long.' }
          : { text, state: 'done', term: { id: 'bt-' + text, text, created_at: new Date().toISOString() } },
      );
      terms = [...results.filter((r) => r.term).map((r) => r.term as BlockedTerm), ...terms];
      return { status: 200, body: { results } };
    },
    '/v1/filters/blocked-terms/bt-1': () => {
      terms = terms.filter((t) => t.id !== 'bt-1');
      return { status: 204, body: null };
    },
    '/v1/filters/check': (_m, body) =>
      (body as { text: string }).text.includes('bit.ly')
        ? { status: 200, body: { rule: 'links', reason: 'Please don’t post links here.' } }
        : { status: 200, body: { rule: '' } },
  });
}

describe('Chat filters', () => {
  it('turns filters on, sets a timeout and words, and saves them', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    input(el, 'Filters are on').click();
    input(el, 'Filter words').click();
    flushSync();
    typed(el.querySelector('textarea')!, 'darn\n  spoil*  \n\n');
    const what = input(el, 'What happens') as unknown as HTMLSelectElement;
    what.value = 'timeout';
    what.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect((input(el, 'For (seconds)') as HTMLInputElement).value).toBe('600');
    input(el, 'Filter links').click();
    flushSync();
    button(el, 'Save').click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/filters' && c.method === 'PUT')!.body as FilterSettings;
    expect(sent.on).toBe(true);
    expect(sent.words).toMatchObject({ on: true, action: 'timeout', seconds: 600, list: ['darn', 'spoil*'] });
    expect(sent.links).toMatchObject({ on: true, action: 'delete', seconds: 0, allowed: ['twitch.tv', 'clips.twitch.tv'] });
    expect(sent.caps.on).toBe(false);
    expect(el.textContent).toContain('2 filters are watching chat from now on.');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('tries a message with the form as it is, and says which filter would catch it', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    typed(input(el, 'Message'), 'see bit.ly/x');
    button(el, 'Try').click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/filters/check')!.body as { text: string; role: string; settings: FilterSettings };
    expect(sent.text).toBe('see bit.ly/x');
    expect(sent.role).toBe('viewer');
    expect(sent.settings.links.allowed).toEqual(['twitch.tv', 'clips.twitch.tv']);
    expect(el.textContent).toContain('Links would catch it: “Please don’t post links here.”');
    typed(input(el, 'Message'), 'hello');
    button(el, 'Try').click();
    await settle(30);
    expect(el.textContent).toContain('No filter that’s ticked would catch it.');
  });

  it('lists what was caught, with Twitch’s answer', async () => {
    engine(caught);
    const el = start();
    await settle(30);
    const items = Array.from(el.querySelectorAll('.caught li')).map((li) => li.textContent?.replace(/\s+/g, ' '));
    expect(items[0]).toContain('Spammer deleted by Links: cheap followers at bit.ly/x Done');
    expect(items[1]).toContain('Loud timed out for 600 s by Capitals');
    expect(items[1]).toContain('Refused: Sign in to Twitch again');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('lists Twitch’s Blocked Terms, copies words both ways, and removes one', async () => {
    const soon = new Date(Date.now() + 3600_000).toISOString();
    const calls = engine([], [
      { id: 'bt-1', text: 'darn', created_at: new Date().toISOString() },
      { id: 'bt-2', text: 'Heck', created_at: new Date().toISOString(), expires_at: soon },
    ]);
    const el = start();
    await settle(30);
    const panel = el.querySelector('[aria-labelledby=blocked-heading]')!;
    const items = () => Array.from(panel.querySelectorAll('.blocked li')).map((li) => li.textContent?.replace(/\s+/g, ' ').trim());
    expect(items()[0]).toContain('darn');
    expect(items()[1]).toContain('Heck AutoMod, until');

    // Twitch's terms into the word list: both, ticking the words filter.
    button(panel, 'Copy Twitch’s terms to my words (2 new)').click();
    flushSync();
    expect(el.querySelector('textarea')!.value).toBe('darn\nheck');
    expect(input(el, 'Filter words').checked).toBe(true);
    expect(panel.textContent).toContain('Put 2 terms in Words above. Save to keep them.');

    // My words onto Twitch: only the ones it hasn't got; a refusal says why.
    typed(el.querySelector('textarea')!, 'darn\nheck\nspoil*\nx');
    button(panel, 'Copy my words to Twitch (2 new)').click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/filters/blocked-terms' && c.method === 'POST')!.body as { texts: string[] };
    expect(sent.texts).toEqual(['spoil*', 'x']);
    expect(panel.textContent).toContain('Added 1 word to Twitch’s Blocked Terms.');
    expect(panel.querySelector('[role=alert]')?.textContent).toContain('x: refused, The length of the term');
    expect(items()[0]).toContain('spoil*');

    (panel.querySelector('[aria-label="Remove darn"]') as HTMLButtonElement).click();
    await settle(30);
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/filters/blocked-terms/bt-1')).toBe(true);
    expect(panel.textContent).toContain('Removed “darn” from Twitch’s Blocked Terms.');
    expect(items().some((i) => i?.startsWith('darn'))).toBe(false);
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('shows Twitch’s Blocked Terms as unknown, with why', async () => {
    mockFetch({
      '/v1/filters': () => ({ status: 200, body: { settings: defaults(), recent: [] } }),
      '/v1/filters/blocked-terms': () => ({ status: 200, body: { terms: [], problem: 'Sign in to Twitch again on Connections and allow StreamGremlin to manage blocked terms.' } }),
    });
    const el = start();
    await settle(30);
    const panel = el.querySelector('[aria-labelledby=blocked-heading]')!;
    expect(panel.textContent).toContain('Unknown: Sign in to Twitch again');
    expect(panel.querySelector('button.button:not([disabled])')?.textContent).toBe('Refresh');
    expect(panel.textContent).not.toContain('Copy my words');
  });

  it('says why when bad settings are refused', async () => {
    mockFetch({
      '/v1/filters': (m) =>
        m === 'PUT'
          ? { status: 400, body: { error: { code: 'invalid_request', message: '"a*b": a * goes only at the start or end' } } }
          : { status: 200, body: { settings: defaults(), recent: [] } },
    });
    const el = start();
    await settle(30);
    button(el, 'Save').click();
    await settle(30);
    expect(el.querySelector('[role=alert]')?.textContent).toContain('a * goes only at the start or end');
  });
});
