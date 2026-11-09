import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import AdsCard from './AdsCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { AdRequest, AdsView } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start(refreshMs = 60000) {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(AdsCard, { target, props: { refreshMs } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const inMin = (m: number) => new Date(Date.now() + m * 60000).toISOString();

const view = (over: Partial<AdsView> = {}): AdsView => ({
  state: 'ok',
  schedule: { snooze_count: 2, next_ad_at: inMin(12), duration: 90, preroll_free_seconds: 0, last_ad_at: inMin(-30) },
  requests: [],
  breaks: [],
  ...over,
});

describe('AdsCard', () => {
  it('is hidden for a channel without ads, and while signed out', async () => {
    for (const state of ['no_channel_points', 'not_signed_in'] as const) {
      mockFetch({ '/v1/ads': () => ({ status: 200, body: view({ state, schedule: null }) }) });
      const el = start();
      await settle(30);
      expect(el.querySelector('section')).toBeNull();
      unmount(comp!);
      comp = undefined;
      document.body.innerHTML = '';
    }
  });

  it('shows the schedule, a running break as an estimate, and what was asked apart from what happened', async () => {
    const reqs: AdRequest[] = [
      { id: 'r2', action: 'commercial', length: 60, requested_by: 'the app', status: 'unknown', created_at: inMin(-1) },
      { id: 'r1', action: 'commercial', length: 30, requested_by: 'the app', status: 'refused', message: 'you need to be live', created_at: inMin(-2) },
    ];
    mockFetch({
      '/v1/ads': () => ({
        status: 200,
        body: view({ requests: reqs, breaks: [{ key: 'b', started_at: inMin(-0.5), duration_seconds: 90, is_automatic: true, end_estimated_at: inMin(1) }] }),
      }),
    });
    const el = start();
    await settle(30);
    const text = el.textContent ?? '';
    expect(text).toContain('Ad break running');
    expect(text).toContain('estimated');
    expect(text).toContain('Snoozes left');
    expect(text).toContain('unclear. It may or may not have started');
    expect(text).toContain('refused, nothing happened. you need to be live');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);
  });

  it('asks before running an ad, sends a key, and says accepted is not running', async () => {
    const calls = mockFetch({
      '/v1/ads': () => ({ status: 200, body: view() }),
      '/v1/ads/commercial': (_m, b) => ({
        status: 200,
        body: { id: 'r', action: 'commercial', length: (b as { length: number }).length, requested_by: 'the app', status: 'accepted', accepted_length: 90, created_at: inMin(0) },
      }),
    });
    const el = start();
    await settle(30);
    button(el, 'Run an ad now')!.click();
    flushSync();
    expect(document.querySelector('[role="alertdialog"]')?.textContent).toContain('Run a 90-second ad now?');
    expect(calls.some((c) => c.path === '/v1/ads/commercial')).toBe(false);
    button(document.body, 'Run the ad')!.click();
    await settle(30);
    const sent = calls.find((c) => c.path === '/v1/ads/commercial')!.body as { length: number; op_key: string };
    expect(sent.length).toBe(90);
    expect(sent.op_key).toBeTruthy();
    expect(el.textContent).toContain('It shows here when Twitch says it began');
  });

  it('shows a refusal in Twitch’s words and blocks running during the gap', async () => {
    const retry = inMin(5);
    mockFetch({
      '/v1/ads': () => ({
        status: 200,
        body: view({ requests: [{ id: 'r', action: 'commercial', length: 90, requested_by: 'x', status: 'accepted', retry_until: retry, created_at: inMin(-3) }] }),
      }),
      '/v1/ads/snooze': () => ({ status: 409, body: { error: { code: 'conflict', message: 'ads: twitch: you have no snoozes left' } } }),
    });
    const el = start();
    await settle(30);
    expect(button(el, 'Run an ad now')!.disabled).toBe(true);
    expect(el.textContent).toContain('you can run another in');
    button(el, 'Snooze the next ad')!.click();
    await settle(30);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('no snoozes left');
  });

  it('shows unknown, never the old schedule, when the engine stops answering', async () => {
    let up = true;
    mockFetch({ '/v1/ads': () => (up ? { status: 200, body: view() } : { status: 503, body: { error: { code: 'unavailable', message: 'down' } } }) });
    const el = start(40);
    await settle(20);
    expect(el.textContent).toContain('Snoozes left');
    up = false;
    await settle(120);
    expect(el.textContent).toContain('Unknown');
    expect(el.textContent).not.toContain('Snoozes left');
  });
});
