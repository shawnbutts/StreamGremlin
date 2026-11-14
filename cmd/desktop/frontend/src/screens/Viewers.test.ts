import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Viewers from './Viewers.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { PointsView, Viewer, ViewerDetail } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start(openId?: string) {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Viewers, { target, props: { refreshMs: 60000, openId } });
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
    '/desktop/save-file': () => ({ status: 200, body: { saved: '/Users/me/ann-streamgremlin.json' } }),
    '/v1/exports/viewers': () => ({ status: 200, body: { schema: 'streamgremlin.viewers-export/v1', viewers: [] } }),
  });
}

describe('Viewers', () => {
  it('links a Twitch viewer to their YouTube account after asking, and unlinks (D-101)', async () => {
    const tube: Viewer = { id: 'v_yt', platform: 'youtube', user_id: 'UCann', login: 'ann.yt', display_name: '@Ann.YT', first_seen: now, last_seen: now, messages: 3, balance: 7 };
    let linked = false;
    const calls = mockFetch({
      '/v1/points': () => ({ status: 200, body: view }),
      '/v1/viewers': () => ({ status: 200, body: { viewers: [ann, tube] } }),
      '/v1/viewers/v_ann': () => ({
        status: 200,
        body: detail(linked ? { balance: 57, link: { id: 'l1', with: tube, linked_at: now, linked_by: 'the app' } } : {}),
      }),
      '/v1/viewers/v_ann/link': (method) => {
        linked = method === 'POST';
        return { status: 200, body: ann };
      },
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('YouTube · 7 points');
    button(el, 'Ann')!.click();
    await settle(30);
    expect(el.textContent).toContain('Not linked.');
    typed(field(el, 'Their name on YouTube'), 'ann');
    await settle(30);
    const list = el.querySelector('[aria-label="Accounts to link"]')!;
    expect(list.textContent).toContain('@Ann.YT');
    expect(list.textContent).not.toContain('Ann 50'); // only the other platform
    button(list, 'Link…')!.click();
    flushSync();
    expect(document.body.textContent).toContain('@Ann.YT’s 7 points join Ann’s 50');
    expect(calls.some((c) => c.path === '/v1/viewers/v_ann/link')).toBe(false);
    button(document.body, 'Link')!.click();
    await settle(30);
    expect(calls.find((c) => c.path === '/v1/viewers/v_ann/link')).toMatchObject({ method: 'POST', body: { with: 'v_yt' } });
    expect(el.textContent).toContain('57 points, shared with @Ann.YT on YouTube');
    expect(el.textContent!.replace(/\s+/g, ' ')).toContain('Linked to @Ann.YT on YouTube by the app');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
    button(el, 'Unlink…')!.click();
    flushSync();
    expect(document.body.textContent).toContain('stay with the Twitch account');
    button(document.body, 'Unlink')!.click();
    await settle(30);
    expect(calls.some((c) => c.path === '/v1/viewers/v_ann/link' && c.method === 'DELETE')).toBe(true);
  });

  it('shows who holds how many, a viewer’s history, and gives points with a request id and a reason', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    expect(el.textContent!.replace(/\s+/g, ' ')).toContain('Twitch · 50 points · last seen');
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

  it('exports a viewer with the system’s save dialog, and forgets after asking', async () => {
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
  });

  it('exports everyone with the save dialog, and opens a viewer it’s sent to', async () => {
    const calls = engine();
    const el = start('v_ann');
    await settle(30);
    expect(el.querySelector('#viewer-heading')?.textContent).toContain('Ann');
    button(el, 'Export everyone')!.click();
    await settle(30);
    const save = calls.find((c) => c.path === '/desktop/save-file')!.body as { name: string; text: string };
    expect(save.name).toBe('streamgremlin-viewers.json');
    expect(JSON.parse(save.text).schema).toBe('streamgremlin.viewers-export/v1');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });
});
