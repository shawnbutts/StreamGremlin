import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Events from './Events.svelte';
import { eventCatalog, mockFetch, settle } from '../test/helpers';
import type { EventRecord } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  // Inside <main>, as in the app, so axe sees the same landmarks.
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Events, { target, props: { idleMs: 5, retryMs: 5 } });
  return target;
}

const rec = (seq: number, text: string, quality: EventRecord['delivery_quality'] = 'confirmed'): EventRecord => ({
  seq,
  event_id: `e${seq}`,
  type: 'chat.message.received',
  schema_version: 1,
  source: 'twitch',
  occurred_at: new Date().toISOString(),
  received_at: new Date().toISOString(),
  actor: { platform: 'twitch', id: '1', display_name: 'Fan' },
  payload: { text },
  delivery_quality: quality,
});

/** A fake inbox: answers like GET /v1/events and POST /v1/events/simulate. */
function inbox(initial: EventRecord[] = []) {
  const stored = [...initial];
  let failing = false;
  const calls = mockFetch({
    '/v1/events/catalog': eventCatalog,
    '/v1/events': (_m, _b, query) => {
      if (failing) return { status: 503, body: { error: { code: 'engine_failed', message: 'Engine stopped.' } } };
      const q = new URLSearchParams(query);
      const head = stored.length ? stored[stored.length - 1].seq : 0;
      if (!q.has('after')) return { status: 200, body: { events: stored.slice(-50), head } };
      const after = Number(q.get('after'));
      return { status: 200, body: { events: stored.filter((e) => e.seq > after), head } };
    },
    '/v1/events/simulate': (_m, body) => {
      const b = body as { type: string; fields: Record<string, string>; request_id: string };
      const ev = rec(stored.length + 1, b.fields.text ?? '', 'synthetic');
      stored.push(ev);
      return { status: 201, body: { event: ev, duplicate: false } };
    },
  });
  return {
    calls,
    push: (ev: EventRecord) => stored.push(ev),
    fail: (f: boolean) => (failing = f),
  };
}

const rows = (el: HTMLElement) => Array.from(el.querySelectorAll('.event-row .event-summary')).map((r) => r.textContent);

describe('Events', () => {
  it('shows stored events newest first, then live ones at the top', async () => {
    const box = inbox([rec(1, 'first'), rec(2, 'second')]);
    const el = start();
    await settle(50);
    expect(rows(el)).toEqual(['Fan: second', 'Fan: first']);
    expect(el.querySelector('[role="status"]')?.textContent).toContain('Listening');
    box.push(rec(3, 'third'));
    await settle(50);
    expect(rows(el)).toEqual(['Fan: third', 'Fan: second', 'Fan: first']);
    // Reads continue from the cursor: never a page that starts at 0 again.
    const later = box.calls.filter((c) => c.path.startsWith('/v1/events?after='));
    expect(later.length).toBeGreaterThan(0);
    expect(later.every((c) => !c.path.includes('after=0'))).toBe(true);
    for (const theme of ['dark', 'light', 'midnight']) {
      document.documentElement.dataset.theme = theme;
      const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
      expect(result.violations.map((v) => v.id), theme).toEqual([]);
    }
  });

  it('says it is not receiving events when the engine stops answering, and recovers', async () => {
    const box = inbox([rec(1, 'first')]);
    const el = start();
    await settle(30);
    box.fail(true);
    await settle(30);
    const status = el.querySelector('[role="status"]')?.textContent ?? '';
    expect(status).toContain('Not receiving events');
    expect(status).not.toContain('Listening');
    box.fail(false);
    box.push(rec(2, 'back'));
    await settle(50);
    expect(el.querySelector('[role="status"]')?.textContent).toContain('Listening');
    expect(rows(el)[0]).toBe('Fan: back');
  });

  it('sends a test event with the form, with a fresh request id each time', async () => {
    const box = inbox();
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Nothing yet');
    const input = Array.from(el.querySelectorAll<HTMLLabelElement>('label.field')).find((l) =>
      l.textContent?.includes('Message'),
    )!.querySelector('input')!;
    input.value = 'testing 1 2 3';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    const button = el.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    button.click();
    await settle(30);
    button.click();
    await settle(50);
    const sims = box.calls.filter((c) => c.path === '/v1/events/simulate');
    expect(sims).toHaveLength(2);
    const [a, b] = sims.map((c) => c.body as { type: string; fields: Record<string, string>; request_id: string });
    expect(a.type).toBe('chat.message.received');
    expect(a.fields.text).toBe('testing 1 2 3');
    expect(a.request_id).not.toBe(b.request_id);
    expect(el.textContent).toContain('Sent a test chat message.');
    expect(rows(el)[0]).toBe('Fan: testing 1 2 3');
    expect(el.querySelector('.event-row .tag')?.textContent).toBe('Test');
  });

  it('switches the form to the chosen event type', async () => {
    inbox();
    const el = start();
    await settle(30);
    const select = el.querySelector('select')!;
    select.value = 'broadcast.scene.changed';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    expect(el.textContent).toContain('New scene');
    expect(el.textContent).not.toContain('Message');
    expect(el.querySelector<HTMLInputElement>('label.field input')?.value).toBe('Starting Soon');
  });

  it('explains a refused test event', async () => {
    mockFetch({
      '/v1/events/catalog': eventCatalog,
      '/v1/events': () => ({ status: 200, body: { events: [], head: 0 } }),
      '/v1/events/simulate': () => ({
        status: 400,
        body: { error: { code: 'invalid_request', message: 'Message is longer than 500 characters' } },
      }),
    });
    const el = start();
    await settle(30);
    el.querySelector<HTMLButtonElement>('button[type="submit"]')!.click();
    await settle(30);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('longer than 500');
  });
});
