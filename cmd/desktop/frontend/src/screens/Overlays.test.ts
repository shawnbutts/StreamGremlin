import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Overlays from './Overlays.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Overlay, OverlayLook } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  // jsdom has no ResizeObserver; the preview scales with one.
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Overlays, { target, props: { refreshMs: 30 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const now = new Date().toISOString();
const look: OverlayLook = { position: 'top', animation: 'slide', text_color: '#ffffff', background: '#081923', opacity: 90, seconds: 6 };
const alerts: Overlay = { name: 'alerts', kind: 'alert', look, created_at: now, updated_at: now, pages: 1, previews: 0 };
const positions = ['top-left', 'top', 'top-right', 'center', 'bottom-left', 'bottom', 'bottom-right'];

function engine(initial: Overlay[], opts: { note?: string; notOpen?: boolean; noLink?: boolean } = {}) {
  const list = initial.map((o) => ({ ...o, look: { ...o.look } }));
  let token = 'tok1';
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/overlays': (method, body) => {
      if (method === 'POST') {
        const o: Overlay = { name: (body as { name: string }).name, kind: 'alert', look: { ...look }, created_at: now, updated_at: now, pages: 0, previews: 0 };
        list.push(o);
        return { status: 201, body: { overlay: o, url: `http://127.0.0.1:7431/o/${token}/` } };
      }
      return {
        status: 200,
        body: { overlays: list, server: 'http://127.0.0.1:7431', server_note: opts.note, positions, animations: ['slide', 'pop'], default_look: look },
      };
    },
  };
  for (const n of ['alerts', 'corner']) {
    routes[`/v1/overlays/${n}`] = (method, body) => {
      const i = list.findIndex((o) => o.name === n);
      if (method === 'DELETE') {
        list.splice(i, 1);
        return { status: 200, body: { deleted: true } };
      }
      list[i] = { ...list[i], look: (body as { look: OverlayLook }).look };
      return { status: 200, body: list[i] };
    };
    routes[`/v1/overlays/${n}/link`] = (method) => {
      if (method === 'POST') token = 'tok2';
      if (opts.noLink && method === 'GET') {
        return { status: 409, body: { error: { code: 'conflict', message: "this overlay's link isn't saved on this computer; make a new link" } } };
      }
      return { status: 200, body: { name: n, url: `http://127.0.0.1:7431/o/${token}/` } };
    };
    routes[`/v1/overlays/${n}/test`] = (_m, _b, query) =>
      opts.notOpen && query !== 'preview=1'
        ? { status: 503, body: { error: { code: 'unavailable', message: "the overlay isn't open in OBS: add its link as a browser source in a scene" } } }
        : { status: 200, body: query === 'preview=1' ? { test: true, previews: 1 } : { shown: true } };
  }
  return { calls: mockFetch(routes), list };
}

async function noViolations(label: string) {
  // The preview frame is the overlay page, served by the engine: checked
  // here as a titled frame, not entered.
  const result = await axe.run(document.body, { iframes: false, rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Overlays', () => {
  it('lists overlays with whether OBS has them open, and previews the chosen one, accessibly', async () => {
    engine([alerts, { ...alerts, name: 'corner', pages: 0, look: { ...look, position: 'bottom-left' } }]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('Open in OBS');
    expect(el.textContent).toContain('Not open in OBS');
    expect(el.textContent).toContain('Bottom left');
    // The preview is the overlay page itself, marked as a preview.
    const frame = el.querySelector('iframe')!;
    expect(frame.getAttribute('src')).toBe('http://127.0.0.1:7431/o/tok1/?preview=1');
    expect(frame.getAttribute('title')).toBe('Preview of alerts');
    // Links are never listed on screen.
    expect(el.textContent).not.toContain('/o/tok1');
    await noViolations('list');
  });

  it('adds an overlay and copies its link to the clipboard', async () => {
    const { calls } = engine([]);
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    const el = start();
    await settle();
    expect(el.textContent).toContain('No overlays yet.');
    const name = el.querySelector<HTMLInputElement>('input[placeholder="alerts"]')!;
    name.value = 'Bad Name';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Add')!.click();
    await settle();
    expect(el.textContent).toContain('A name is lowercase letters');
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
    name.value = 'alerts';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Add')!.click();
    await settle(80);
    expect(calls.find((c) => c.method === 'POST')).toMatchObject({ path: '/v1/overlays', body: { name: 'alerts', kind: 'alert' } });
    expect(el.textContent).toContain('Added alerts.');
    button(el, 'Copy link')!.click();
    await settle();
    expect(writeText).toHaveBeenCalledWith('http://127.0.0.1:7431/o/tok1/');
    expect(el.textContent).toContain('width 1920, height 1080');
  });

  it('shows the link to copy by hand when the clipboard is refused', async () => {
    engine([alerts]);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText: async () => Promise.reject(new Error('denied')) } });
    const el = start();
    await settle();
    button(el, 'Copy link')!.click();
    await settle();
    expect(el.querySelector<HTMLInputElement>('input[readonly]')!.value).toBe('http://127.0.0.1:7431/o/tok1/');
  });

  it('saves a look, and shows samples in the preview and in OBS', async () => {
    const { calls } = engine([alerts]);
    const el = start();
    await settle();
    expect(button(el, 'Save look')!.disabled).toBe(true);
    const where = el.querySelector<HTMLSelectElement>('select')!;
    where.value = 'bottom-right';
    where.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    button(el, 'Save look')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ path: '/v1/overlays/alerts', body: { look: { position: 'bottom-right', seconds: 6 } } });
    expect(el.textContent).toContain("Saved alerts's look.");
    button(el, 'Preview an alert')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/overlays/alerts/test?preview=1')).toBe(true);
    button(el, 'Sample in OBS')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/overlays/alerts/test')).toBe(true);
    expect(el.textContent).toContain('A sample alert is showing on alerts in OBS.');
  });

  it("says why a sample can't show, and when the usual address is taken", async () => {
    engine([{ ...alerts, pages: 0 }], { notOpen: true, note: 'The usual address, 127.0.0.1:7431, is taken by another program' });
    const el = start();
    await settle();
    expect(el.textContent).toContain('is taken by another program');
    button(el, 'Sample in OBS')!.click();
    await settle();
    expect(el.querySelector('.error')?.textContent).toContain("isn't open in OBS");
  });

  it('makes a new link and deletes, each after asking', async () => {
    const { calls } = engine([alerts], { noLink: true });
    const el = start();
    await settle();
    expect(el.textContent).toContain("link isn't saved on this computer");
    button(el, 'New link')!.click();
    flushSync();
    let dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('The old link stops working at once.');
    button(dialog, 'Make a new link')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/overlays/alerts/link')).toBe(true);
    expect(el.textContent).toContain('alerts has a new link.');
    button(el, 'Delete')!.click();
    flushSync();
    dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Its link stops working');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/overlays/alerts')).toBe(true);
    expect(el.textContent).toContain('No overlays yet.');
  });
});
