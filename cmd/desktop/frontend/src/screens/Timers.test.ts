import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Timers from './Timers.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { TimerStatus } from '../lib/api';

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
  comp = mount(Timers, { target, props: { refreshMs: 30 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

function setValue(el: HTMLInputElement | HTMLSelectElement, v: string) {
  el.value = v;
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
}

function check(el: HTMLInputElement, on: boolean) {
  el.checked = on;
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

const now = new Date().toISOString();
const soon = new Date(Date.now() + 5 * 60_000).toISOString();

const socials: TimerStatus = {
  id: 'socials', name: 'Socials', enabled: true, every_seconds: 600, up_to_seconds: 900, chat_lines: 5, live_only: true, once: false,
  counter: 3, last_fired_at: now, created_at: now, updated_at: now, next_at: soon,
};
const nightly: TimerStatus = {
  id: 'nightly', name: 'Nightly', enabled: false, cron: '0 20 * * *', chat_lines: 0, live_only: false, once: false,
  counter: 0, created_at: now, updated_at: now, waiting: 'off',
};

function engine(initial: TimerStatus[], opts: { refuse?: boolean; live?: boolean } = {}) {
  const timers = [...initial];
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/timers': () => ({ status: 200, body: { timers, live: opts.live } }),
    '/v1/workflows': () => ({
      status: 200,
      body: {
        workflows: [
          { id: 'timer.socials', name: 'Socials message', enabled: true, version: 1, updated_at: now,
            triggers: [{ type: 'timers.timer.fired', where: { timer_id: 'socials' } }] },
        ],
      },
    }),
  };
  for (const t of ['socials', 'nightly', 'raid-reminder', 'timer']) {
    routes[`/v1/timers/${t}`] = (method, body) => {
      if (method === 'DELETE') {
        timers.splice(timers.findIndex((x) => x.id === t), 1);
        return { status: 204, body: null };
      }
      if (opts.refuse) return { status: 500, body: { error: { code: 'internal', message: 'the database is busy' } } };
      const b = body as TimerStatus;
      const i = timers.findIndex((x) => x.id === t);
      const saved = { ...(i >= 0 ? timers[i] : { counter: 0, created_at: now, updated_at: now }), ...b, next_at: soon } as TimerStatus;
      if (!b.cron) delete saved.cron;
      if (i >= 0) timers[i] = saved;
      else timers.push(saved);
      return { status: i >= 0 ? 200 : 201, body: saved };
    };
    routes[`/v1/timers/${t}/fire`] = () => {
      const x = timers.find((x) => x.id === t)!;
      x.counter++;
      return { status: 200, body: { timer: x } };
    };
  }
  return { calls: mockFetch(routes), timers };
}

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Timers', () => {
  it('lists timers in words, with what they start, accessibly', async () => {
    engine([nightly, socials], { live: false });
    const el = start();
    await settle();
    const text = el.textContent!;
    expect(text).toContain('Every 10 min to 15 min, at random, after 5 chat lines from viewers, only while you’re live.');
    expect(text).toContain('Next in 5 min');
    expect(text).toContain('its turn passes unless you’re live');
    expect(text).toContain('Gone off 3 times');
    expect(text).toContain('Starts: “Socials message”');
    expect(text).toContain('On the schedule 0 20 * * *');
    expect(text).toContain('No workflow uses it yet.');
    expect(text).toContain('OBS says you’re not live');
    expect(button(el, 'Make a workflow')).toBeTruthy(); // only for Nightly
    await noViolations('list');
    button(el, 'New timer')!.click();
    flushSync();
    await noViolations('form');
  });

  it('makes a timer from the form, sending only what can be set', async () => {
    const { calls } = engine([socials]);
    const el = start();
    await settle();
    button(el, 'New timer')!.click();
    flushSync();
    const form = el.querySelector('form')!;
    setValue(form.querySelector<HTMLInputElement>('input[placeholder="Socials"]')!, 'Raid reminder!');
    const [every] = form.querySelectorAll<HTMLInputElement>('input[type="number"]');
    setValue(every, '5'); // too often in seconds
    setValue(form.querySelector('select')!, 'seconds');
    flushSync();
    button(form, 'Save')!.click();
    flushSync();
    expect(form.textContent).toContain('every 10 seconds to 24 hours');
    setValue(form.querySelector('select')!, 'minutes');
    check(Array.from(form.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find((c) => c.parentElement?.textContent?.includes('At random'))!, true);
    flushSync();
    setValue(form.querySelectorAll<HTMLInputElement>('input[type="number"]')[1], '8'); // up to
    flushSync();
    button(form, 'Save')!.click();
    await settle();
    const put = calls.find((c) => c.method === 'PUT')!;
    expect(put.path).toBe('/v1/timers/raid-reminder');
    expect(put.body).toEqual({ id: 'raid-reminder', name: 'Raid reminder!', enabled: true, every_seconds: 300, up_to_seconds: 480,
      chat_lines: 0, live_only: true, once: false });
    expect(el.textContent).toContain('Saved “Raid reminder!”. No workflow uses it yet');
    expect(el.textContent).toContain('Every 5 min to 8 min, at random');
  });

  it('changes a timer to a schedule, keeping its id', async () => {
    const { calls } = engine([socials]);
    const el = start();
    await settle();
    button(el, 'Edit')!.click();
    flushSync();
    const form = el.querySelector('form')!;
    expect(form.querySelector<HTMLInputElement>('input[placeholder="Socials"]')!.value).toBe('Socials');
    expect(form.textContent).toContain('socials');
    const cron = form.querySelector<HTMLInputElement>('input[value="cron"]')!;
    cron.checked = true;
    cron.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    button(form, '@hourly')!.click(); // an example fills it in
    flushSync();
    button(form, 'Save')!.click();
    await settle();
    const put = calls.find((c) => c.method === 'PUT')!;
    expect(put.path).toBe('/v1/timers/socials');
    expect(put.body).toMatchObject({ cron: '@hourly', chat_lines: 5, live_only: true });
    expect(put.body).not.toHaveProperty('every_seconds');
    expect(put.body).not.toHaveProperty('counter');
  });

  it('turns a timer off, showing what the engine says when that fails', async () => {
    const { calls } = engine([socials], { refuse: true });
    const el = start();
    await settle();
    const sw = el.querySelector<HTMLInputElement>('input[role="switch"]')!;
    check(sw, false);
    await settle();
    expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ enabled: false });
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('the database is busy');
    expect(el.querySelector<HTMLInputElement>('input[role="switch"]')!.checked).toBe(true);
  });

  it('runs a timer now', async () => {
    const { calls } = engine([socials]);
    const el = start();
    await settle();
    button(el, 'Run now')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/timers/socials/fire')).toBe(true);
    expect(el.textContent).toContain('“Socials” went off');
    expect(el.textContent).toContain('Gone off 4 times');
  });

  it('asks before deleting, saying which workflows stay', async () => {
    engine([socials]);
    const el = start();
    await settle();
    button(el, 'Delete')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('“Socials” is deleted for good. “Socials message” is kept, but won’t start from it any more.');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(el.textContent).toContain('Deleted “Socials”.');
    expect(el.textContent).toContain('No timers yet.');
  });

  it('makes a workflow for a timer that has none, with the timer chosen', async () => {
    engine([socials, nightly]);
    const palette = {
      triggers: [{ type: 'timers.timer.fired', label: 'A timer goes off', category: 'Timers', help: 'One of your timers goes off.',
        match: [{ path: 'timer_id', label: 'Timer', kind: 'timer' }],
        values: [{ token: 'counter', label: 'How many times it has gone off', expr: 'event.payload.counter' }] }],
      actions: [{ name: 'chat.send', label: 'Say in chat', description: 'Sends a message.', group: 'Twitch', external: true,
        params: [{ name: 'message', label: 'Message', type: 'text', editor: 'message', required: true }] }],
    };
    const fetchMock = globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> };
    const inner = fetchMock.getMockImplementation();
    vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) =>
      p === '/v1/palette' ? new Response(JSON.stringify(palette), { status: 200 }) : inner(p, i)));
    const el = start();
    await settle();
    button(el, 'Make a workflow')!.click();
    await settle();
    expect(el.textContent).toContain('A workflow for a timer');
    const timerPick = Array.from(el.querySelectorAll('select')).find((s) => s.textContent?.includes('Any timer'))!;
    expect(timerPick.value).toBe('nightly');
    expect(Array.from(el.querySelectorAll('input')).some((i) => i.value === 'Nightly message')).toBe(true);
    await noViolations('make a workflow');
  });
});
