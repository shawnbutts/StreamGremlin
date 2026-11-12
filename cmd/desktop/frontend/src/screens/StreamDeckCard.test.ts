import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import StreamDeckCard from './StreamDeckCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { PairedClient, PairingState } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

const now = new Date().toISOString();
const later = new Date(Date.now() + 5 * 60_000).toISOString();
const desk: PairedClient = { id: 'cl_1', kind: 'stream_deck', name: 'Desk deck', created_at: now, last_used_at: now };

/** A fake engine: the paired decks and one pairing, moved on by the test. */
function engine(opts: { clients?: PairedClient[]; failList?: boolean } = {}) {
  const clients = [...(opts.clients ?? [])];
  let state: PairingState = { state: 'none', address: 'http://127.0.0.1:7430' };
  const calls = mockFetch({
    '/v1/clients': () => (opts.failList ? { status: 503, body: { error: { code: 'unavailable', message: 'engine busy' } } } : { status: 200, body: { clients } }),
    '/v1/clients/cl_1': () => {
      clients.splice(0, 1);
      return { status: 204, body: null };
    },
    '/v1/clients/pairing': (method) => {
      if (method === 'POST') {
        state = { state: 'waiting', kind: 'stream_deck', code: 'K7QX-M3PA', expires_at: later, address: state.address };
        return { status: 201, body: { code: 'K7QX-M3PA', expires_at: later } };
      }
      if (method === 'DELETE') {
        state = { state: 'none', address: state.address };
        return { status: 204, body: null };
      }
      return { status: 200, body: state };
    },
  });
  return {
    calls,
    pairNow: (c: PairedClient) => {
      clients.unshift(c);
      state = { state: 'paired', kind: 'stream_deck', client: c, address: state.address };
    },
    set: (s: PairingState['state']) => (state = { state: s, kind: 'stream_deck', address: state.address }),
  };
}

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(StreamDeckCard, { target, props: { pollMs: 20 } });
  return target;
}

describe('StreamDeckCard', () => {
  it('pairs a Stream Deck with a code, and says when it’s paired', async () => {
    const e = engine();
    const el = start();
    await settle();
    expect(el.textContent).toContain('No Stream Deck is paired yet.');
    expect(el.textContent).toContain('can only see and press your buttons');
    button(el, 'Pair a Stream Deck')!.click();
    await settle();
    const code = el.querySelector('.pair-code')!;
    expect(code.textContent).toBe('K7QX-M3PA');
    expect(el.textContent).toContain('http://127.0.0.1:7430');
    expect(e.calls.find((c) => c.method === 'POST')?.body).toEqual({ kind: 'stream_deck' });
    await noViolations('waiting');
    e.pairNow({ ...desk, name: 'Studio deck' });
    await settle(80);
    expect(el.querySelector('.pair-code')).toBeNull();
    expect(el.querySelector('[role="status"]')!.textContent).toContain('Paired “Studio deck”.');
    expect(el.textContent).toContain('Studio deck');
  });

  it('says when the code ran out or too many wrong codes were tried, and can stop pairing', async () => {
    const e = engine();
    const el = start();
    await settle();
    button(el, 'Pair a Stream Deck')!.click();
    await settle();
    e.set('expired');
    await settle(80);
    expect(el.textContent).toContain('The code ran out before it was used.');
    button(el, 'Pair a Stream Deck')!.click();
    await settle();
    e.set('locked');
    await settle(80);
    expect(el.textContent).toContain('Too many wrong codes were tried');
    button(el, 'Pair a Stream Deck')!.click();
    await settle();
    button(el, 'Stop pairing')!.click();
    await settle();
    expect(e.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/clients/pairing')).toBe(true);
    expect(el.querySelector('.pair-code')).toBeNull();
  });

  it('lists paired decks and forgets one after asking', async () => {
    const e = engine({ clients: [desk] });
    const el = start();
    await settle();
    const item = el.querySelector('.deck-list li')!;
    expect(item.textContent).toContain('Desk deck');
    expect(item.textContent).toContain('Last used');
    await noViolations('list');
    button(item, 'Forget…')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Forget “Desk deck”?');
    button(dialog, 'Cancel')!.click();
    flushSync();
    expect(e.calls.some((c) => c.method === 'DELETE')).toBe(false);
    button(item, 'Forget…')!.click();
    flushSync();
    button(document.querySelector('[role="alertdialog"]')!, 'Forget')!.click();
    await settle();
    expect(e.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/clients/cl_1')).toBe(true);
    expect(el.textContent).toContain('Forgot “Desk deck”.');
    expect(el.textContent).toContain('No Stream Deck is paired yet.');
  });

  it('says unknown when the engine can’t list them', async () => {
    engine({ failList: true });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Unknown: engine busy');
    expect(el.textContent).not.toContain('No Stream Deck is paired yet.');
  });
});
