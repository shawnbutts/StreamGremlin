import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import BanLists from './BanLists.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { BanList } from '../lib/api';

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
  comp = mount(BanLists, { target, props: { refreshMs: 60000 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

async function noViolations(label: string) {
  const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

const now = new Date().toISOString();
const list = (over: Partial<BanList> = {}): BanList => ({
  id: 'bl_1', kind: 'ban', name: 'Bots', reason: 'Ban list: Bots', phase: 'ready', paused: false, pause_live: true, keep_points: true,
  file_name: 'bots.txt', skipped: 2, created_at: now, updated_at: now, words: 'Checked: confirm to start.',
  counts: { waiting: 1200, already: 30, not_found: 400, kept: 3, removed: 5 }, left: 0, progress: { waiting_live: false, per_minute: 0 },
  ...over,
});

describe('Ban lists', () => {
  it('imports a file, shows what it will do, and bans only once confirmed', async () => {
    let current = list();
    const calls = mockFetch({
      '/v1/ban-lists': (method, body) => {
        if (method === 'POST') return { status: 200, body: { ...current, name: (body as { name: string }).name, phase: 'reading' } };
        return { status: 200, body: { lists: [current] } };
      },
      '/v1/ban-lists/bl_1/entries': () => ({ status: 200, body: { entries: [{ name: 'spambot1', login: 'spambot1', state: 'waiting' }] } }),
      '/v1/ban-lists/bl_1/confirm': (_m, body) => {
        current = list({ phase: 'banning', words: 'Banning…', counts: { waiting: 900, banned: 300 }, left: 900,
          progress: { waiting_live: false, per_minute: 400, finish: new Date(Date.now() + 3 * 3600_000).toISOString() } });
        return { status: 200, body: { ...current, asked: body } };
      },
      '/desktop/choose-file': () => ({ status: 200, body: { path: '/home/me/hate-raid.txt' } }),
    });
    const el = start();
    await settle();
    // The checked list: counts, a sample, and nothing banned yet.
    expect(el.textContent).toContain('1,200 to ban');
    expect(el.textContent).toContain('400 not found');
    expect(el.textContent).toContain('3 left alone (you, your mods and VIPs, viewers with points)');
    expect(el.textContent).toContain('For example: spambot1');
    expect(el.textContent).toContain('2 lines weren’t a Twitch name');
    await noViolations('ready');

    // Importing: choose a file, the name follows it.
    button(el, 'Choose file…')!.click();
    await settle();
    const name = el.querySelector<HTMLInputElement>('form[aria-label="Import a ban list"] input')!;
    expect(name.value).toBe('hate-raid');
    typed(name, 'Raid bots');
    el.querySelector<HTMLFormElement>('form[aria-label="Import a ban list"]')!.requestSubmit();
    await settle();
    expect(calls.find((c) => c.method === 'POST' && c.path === '/v1/ban-lists')?.body).toEqual({ name: 'Raid bots', path: '/home/me/hate-raid.txt' });
    expect(el.textContent).toContain('Nothing is banned until you confirm.');

    // Confirming, with the removed names unbanned too.
    const removed = Array.from(el.querySelectorAll<HTMLLabelElement>('label')).find((l) => l.textContent?.includes('Also unban the 5'))!;
    removed.querySelector('input')!.click();
    flushSync();
    button(el, 'Start banning')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/ban-lists/bl_1/confirm')?.body).toEqual({ unban_removed: true, reban_by_hand: false });
    expect(el.textContent).toContain('Done: 300 of 1,200');
    expect(el.textContent).toContain('400 a minute, finished in about 3 hours');
    expect(button(el, 'Pause')).toBeTruthy();
    await noViolations('banning');
  });

  it('waits while live, says why it paused, and undoes only after asking', async () => {
    let current = list({ phase: 'banning', paused: true, problem: 'Sign in to Twitch again', words: 'Paused: Sign in to Twitch again', counts: { waiting: 10 }, left: 10 });
    const calls = mockFetch({
      '/v1/ban-lists': () => ({ status: 200, body: { lists: [current] } }),
      '/v1/ban-lists/bl_1/resume': () => {
        current = list({ phase: 'done', words: 'Done.', counts: { banned: 10 }, left: 0 });
        return { status: 200, body: current };
      },
      '/v1/ban-lists/bl_1/undo': () => ({ status: 200, body: list({ phase: 'undoing', words: 'Unbanning what this list banned…' }) }),
    });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Paused: Sign in to Twitch again');
    button(el, 'Resume')!.click();
    await settle();
    expect(el.textContent).toContain('10 banned by this list');
    button(el, 'Undo…')!.click();
    flushSync();
    expect(document.body.textContent).toContain('This unbans the 10 accounts this list banned');
    expect(calls.some((c) => c.path.endsWith('/undo'))).toBe(false);
    button(document.body, 'Undo the list')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/ban-lists/bl_1/undo' && c.method === 'POST')).toBe(true);
  });

  it('says a list waits until you’re offline', async () => {
    mockFetch({
      '/v1/ban-lists': () => ({
        status: 200,
        body: { lists: [list({ phase: 'banning', words: 'Banning…', left: 5, progress: { waiting_live: true, per_minute: 0 } })] },
      }),
    });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Waiting until you’re offline on Twitch');
  });

  it('finds follow bots, ticks the ones in a burst, and blocks only after asking (D-109)', async () => {
    const calls = mockFetch({
      '/v1/ban-lists': (m, body) => (m === 'POST' ? { status: 200, body: { id: 'bl_2', ...(body as object) } } : { status: 200, body: { lists: [] } }),
      '/v1/follow-bots': () => ({
        status: 200,
        body: {
          options: { hours: 24, days: 7, burst: 5, minutes: 10 }, follows: 40, unknown: 1,
          suspects: [
            { user_id: '1', login: 'bot_a', display_name: 'Bot_A', followed_at: now, age_days: 0, in_burst: true },
            { user_id: '2', login: 'bot_b', display_name: 'Bot_B', followed_at: now, age_days: 1, in_burst: true },
            { user_id: '3', login: 'maybe', display_name: 'Maybe', followed_at: now, age_days: 3, in_burst: false },
          ],
        },
      }),
    });
    const el = start();
    await settle();
    const form = el.querySelector<HTMLFormElement>('form[aria-label="Find follow bots"]')!;
    typed(Array.from(form.querySelectorAll('input'))[0] as HTMLInputElement, '48');
    form.requestSubmit();
    await settle();
    expect(calls.find((c) => c.path.startsWith('/v1/follow-bots'))!.path).toBe('/v1/follow-bots?hours=48&days=7&burst=5&minutes=10');
    expect(el.textContent).toContain('3 of 40 follows look like bots (1 couldn’t be checked)');
    expect(el.textContent).toContain('Bot_A (made the same day when they followed, in a burst)');
    const boxes = Array.from(el.querySelectorAll<HTMLInputElement>('ul[aria-label="Suspected follow bots"] input'));
    expect(boxes.map((b) => b.checked)).toEqual([true, true, false]);
    await noViolations('review');
    button(el, 'Block 2…')!.click();
    flushSync();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/ban-lists')).toBe(false);
    button(document.body, 'Block them')!.click();
    await settle();
    const body = calls.find((c) => c.method === 'POST' && c.path === '/v1/ban-lists')!.body as { names: string[]; block: boolean; start: boolean };
    expect(body.names).toEqual(['bot_a', 'bot_b']);
    expect(body.block).toBe(true);
    expect(body.start).toBe(true);
    expect(el.textContent).toContain('Blocking 2');
  });
});
