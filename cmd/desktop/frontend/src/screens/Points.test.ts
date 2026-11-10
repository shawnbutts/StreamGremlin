import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Points from './Points.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { EarningRules, PointsGive, PointsView, Viewer, ViewerDetail } from '../lib/api';

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
  comp = mount(Points, { target, props: { refreshMs: 60000 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const field = (el: ParentNode, label: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.querySelector('span')?.textContent?.trim() === label)!.querySelector('input') as HTMLInputElement;
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

const now = new Date().toISOString();
const ann: Viewer = { id: 'v_ann', platform: 'twitch', user_id: '1', login: 'ann', display_name: 'Ann', first_seen: now, last_seen: now, messages: 12, balance: 50 };
const detail = (over: Partial<ViewerDetail> = {}): ViewerDetail => ({
  ...ann,
  history: [
    { id: 'tx1', kind: 'adjust', currency: 'points', viewer: 'v_ann', amount: 50, memo: 'welcome', by: 'the app', created_at: now },
  ],
  ...over,
});
const view: PointsView = { currency: { id: 'points', name: 'points', allow_negative: false }, summary: { transactions: 1, viewers: 1, issued: 50 }, top: [ann] };

const earning: EarningRules = {
  on: false,
  chat: { amount: 1, every_seconds: 60, live_only: true },
  time: { on: false, every_minutes: 5, active: 10, active_minutes: 10, quiet: 3 },
  follow: 50,
  sub: { tier1: 500, tier2: 1000, tier3: 2500 },
  gift: 250,
  bits: 10,
};

const give: PointsGive = {
  id: 'g_1', audience: 'chat', words: 'everyone in chat', amount: 10, memo: 'raid', by: 'the app', viewers: 3, total: 30, created_at: now,
  paid_at: now, applied: true,
};

function engine(opts: { short?: boolean; gives?: PointsGive[]; listDown?: boolean } = {}) {
  return mockFetch({
    '/v1/points': () => ({ status: 200, body: view }),
    '/v1/viewers': () => ({ status: 200, body: { viewers: [ann] } }),
    '/v1/viewers/v_ann': () => ({ status: 200, body: detail() }),
    '/v1/viewers/v_ann/points': (_m, body) =>
      opts.short && (body as { amount: number }).amount < 0
        ? { status: 409, body: { error: { code: 'conflict', message: 'not enough points: has 50, needs 80' } } }
        : { status: 200, body: { balance: 60 } },
    '/v1/points/transactions/tx1/undo': () => ({ status: 200, body: { balance: 0 } }),
    '/v1/viewers/v_ann/export': () => ({ status: 200, body: { schema: 'streamgremlin.viewer-export/v1' } }),
    '/v1/viewers/v_ann/forget': () => ({ status: 200, body: { ...ann, forgotten_at: now } }),
    '/v1/points/currency': (_m, body) => ({ status: 200, body: { id: 'points', name: (body as { name: string }).name } }),
    '/v1/points/audit': () => ({ status: 200, body: { ok: true, problems: [] } }),
    '/desktop/save-file': () => ({ status: 200, body: { saved: '/Users/me/ann-streamgremlin.json' } }),
    '/v1/points/earning': (m, body) => ({ status: 200, body: { rules: m === 'PUT' ? body : earning, live: 'unknown' } }),
    '/v1/points/gives': (m, body) =>
      m === 'POST'
        ? { status: 200, body: { ...give, ...(body as object), total: 30, viewers: 3, applied: true } }
        : { status: 200, body: { gives: opts.gives ?? [] } },
    '/v1/points/gives/preview': (_m, body) =>
      (body as { audience: string }).audience === 'subs'
        ? { status: 200, body: { audience: 'subs', viewers: 0, sample: [], note: 'Twitch listed no subscribers. A channel that isn’t an Affiliate or Partner has none.' } }
        : (body as { audience: string }).audience === 'chat' && opts.listDown
          ? { status: 502, body: { error: { code: 'unavailable', message: 'Twitch’s list couldn’t be read, so nothing was given: Twitch is down' } } }
          : { status: 200, body: { ...(body as object), viewers: 12, sample: ['Ann', 'Bob'] } },
    '/v1/points/gives/g_1/undo': () => ({ status: 200, body: { give, undone: 2, short: ['Bob'] } }),
    '/v1/exports/viewers': () => ({ status: 200, body: { schema: 'streamgremlin.viewers-export/v1', viewers: [] } }),
  });
}

describe('Points', () => {
  it('shows who holds how many, a viewer’s history, and gives points with a request id and a reason', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('1 viewer holds 50 points');
    button(el, 'Ann')!.click();
    await settle(30);
    expect(el.textContent).toContain('First seen');
    expect(el.textContent).toContain('+50');
    expect(el.textContent).toContain('by hand: welcome');
    typed(field(el, 'How many'), '10');
    typed(field(el, 'Reason'), 'good question');
    button(el, 'Give')!.click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/viewers/v_ann/points')!.body as Record<string, unknown>;
    expect(sent).toMatchObject({ amount: 10, reason: 'good question' });
    expect(typeof sent.request_id).toBe('string');
    expect(el.textContent).toContain('Gave Ann 10 points.');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('says plainly when a viewer hasn’t enough, and undoes only after asking', async () => {
    const calls = engine({ short: true });
    const el = start();
    await settle(30);
    button(el, 'Ann')!.click();
    await settle(30);
    typed(field(el, 'How many'), '80');
    button(el, 'Take')!.click();
    await settle(30);
    expect(el.querySelector('[role=alert]')?.textContent).toContain('not enough points: has 50, needs 80');
    button(el, 'Undo')!.click();
    flushSync();
    expect(document.body.textContent).toContain('It can be undone only once.');
    expect(calls.some((c) => c.path.endsWith('/undo'))).toBe(false);
    const confirm = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).filter((b) => b.textContent?.trim() === 'Undo');
    confirm[confirm.length - 1].click();
    await settle(30);
    expect(calls.some((c) => c.path === '/v1/points/transactions/tx1/undo')).toBe(true);
  });

  it('exports with the system’s save dialog, forgets after asking, renames, and checks the ledger', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    button(el, 'Ann')!.click();
    await settle(30);
    button(el, 'Export their record')!.click();
    await settle(30);
    const save = calls.find((c) => c.path === '/desktop/save-file')!.body as { name: string; text: string };
    expect(save.name).toBe('ann-streamgremlin.json');
    expect(JSON.parse(save.text).schema).toBe('streamgremlin.viewer-export/v1');
    expect(el.textContent).toContain('Saved Ann’s record'.replace('’', "'"));
    button(el, 'Forget them')!.click();
    flushSync();
    expect(calls.some((c) => c.path.endsWith('/forget'))).toBe(false);
    button(document.body, 'Forget')!.click();
    await settle(30);
    expect(calls.some((c) => c.path === '/v1/viewers/v_ann/forget')).toBe(true);
    typed(field(el, 'Call them'), 'Gold');
    button(el, 'Rename')!.click();
    await settle(30);
    expect(calls.find((c) => c.path === '/v1/points/currency')!.body).toEqual({ name: 'Gold' });
    button(el, 'Check the ledger')!.click();
    await settle(30);
    expect(el.querySelector('.state-ok')?.textContent).toContain('Every change balances');
  });

  it('turns earning on and saves the rules, and says when it’s unknown whether you’re live', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Unknown whether you’re live');
    expect(el.textContent).toContain('Chat earns nothing until you go live.');
    const on = Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.includes('Viewers earn points'))!.querySelector('input')!;
    on.click();
    flushSync();
    typed(field(el, 'A first follow (once each)'), '75');
    typed(field(el, 'Each 100 bits'), '0');
    button(el, 'Save')!.click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/points/earning' && c.method === 'PUT')!.body as EarningRules;
    expect(sent).toMatchObject({ on: true, follow: 75, bits: 0, chat: { amount: 1, every_seconds: 60, live_only: true } });
    expect(el.textContent).toContain('Saved. Viewers earn from now on.');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('turns on time in chat, and says why quiet viewers weren’t paid', async () => {
    const calls = mockFetch({
      '/v1/points': () => ({ status: 200, body: view }),
      '/v1/viewers': () => ({ status: 200, body: { viewers: [] } }),
      '/v1/points/gives': () => ({ status: 200, body: { gives: [] } }),
      '/v1/points/earning': (m, body) => ({
        status: 200,
        body: {
          rules: m === 'PUT' ? body : { ...earning, on: true },
          live: 'live',
          last_tick: { at: now, active: 2, quiet: 0, note: 'Twitch’s chatter list couldn’t be read (sign in again), so only viewers who chatted earned' },
        },
      }),
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Last paid');
    expect(el.textContent).toContain('2 chatting, 0 quiet');
    expect(el.textContent).toContain('so only viewers who chatted earned');
    const on = Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.includes('Pay for time in chat'))!.querySelector('input')!;
    on.click();
    flushSync();
    typed(field(el, 'If they’re in chat but quiet'), '2');
    button(el, 'Save')!.click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/points/earning' && c.method === 'PUT')!.body as EarningRules;
    expect(sent.time).toEqual({ on: true, every_minutes: 5, active: 10, active_minutes: 10, quiet: 2 });
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('shows who’d get points before giving to everyone, and gives once with a request id', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    const radio = (label: string) =>
      Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.trim() === label)!.querySelector('input') as HTMLInputElement;
    radio('Everyone who chatted lately').click();
    flushSync();
    typed(field(el, 'In the last (minutes)'), '20');
    typed(field(el, 'How many each'), '5');
    typed(field(el, 'Why'), 'thanks for hanging out');
    expect(button(el, 'Give 12 viewers 5 each')).toBeUndefined();
    button(el, 'See who')!.click();
    await settle(30);
    expect(calls.find((c) => c.path === '/v1/points/gives/preview')!.body).toEqual({ audience: 'recent', minutes: 20 });
    expect(el.textContent).toContain('12 viewers: Ann, Bob and 10 more. 5 points each, 60 in all.');
    button(el, 'Give 12 viewers 5 each')!.click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/points/gives' && c.method === 'POST')!.body as Record<string, unknown>;
    expect(sent).toMatchObject({ audience: 'recent', minutes: 20, amount: 5, reason: 'thanks for hanging out' });
    expect(typeof sent.request_id).toBe('string');
    expect(el.textContent).toContain('Gave 3 viewers');
    // Subscribers: an empty list says why, and there's nothing to give.
    radio('Subscribers').click();
    el.querySelector('fieldset')!.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    button(el, 'See who')!.click();
    await settle(30);
    expect(el.textContent).toContain('Twitch listed no subscribers');
    expect(Array.from(el.querySelectorAll('button')).some((b) => b.textContent?.startsWith('Give 0'))).toBe(false);
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('says plainly when Twitch’s list can’t be read, and gives nothing', async () => {
    const calls = engine({ listDown: true });
    const el = start();
    await settle(30);
    button(el, 'See who')!.click();
    await settle(30);
    expect(el.querySelector('[role=alert]')?.textContent).toContain('nothing was given');
    expect(calls.some((c) => c.path === '/v1/points/gives' && c.method === 'POST')).toBe(false);
  });

  it('undoes a give after asking, naming who keeps theirs, and exports everyone with the save dialog', async () => {
    const calls = engine({ gives: [give] });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('10 each to 3 (everyone in chat): raid');
    el.querySelector<HTMLButtonElement>('[aria-label="Undo the give to everyone in chat"]')!.click();
    flushSync();
    expect(document.body.textContent).toContain('nobody goes below zero');
    expect(calls.some((c) => c.path.endsWith('/undo'))).toBe(false);
    const confirm = Array.from(document.body.querySelectorAll<HTMLButtonElement>('button')).filter((b) => b.textContent?.trim() === 'Undo');
    confirm[confirm.length - 1].click();
    await settle(30);
    expect(calls.some((c) => c.path === '/v1/points/gives/g_1/undo')).toBe(true);
    expect(el.textContent).toContain('Took it back from 2 viewers.');
    expect(el.querySelector('[role=alert]')?.textContent).toContain('Bob spent some already and keep theirs');
    button(el, 'Export everyone')!.click();
    await settle(30);
    const save = calls.find((c) => c.path === '/desktop/save-file')!.body as { name: string; text: string };
    expect(save.name).toBe('streamgremlin-viewers.json');
    expect(JSON.parse(save.text).schema).toBe('streamgremlin.viewers-export/v1');
  });
});
