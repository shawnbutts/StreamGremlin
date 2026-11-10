import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Points from './Points.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { PointsView, Viewer, ViewerDetail } from '../lib/api';

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

function engine(opts: { short?: boolean } = {}) {
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
});
