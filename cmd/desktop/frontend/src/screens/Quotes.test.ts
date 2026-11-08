import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Quotes from './Quotes.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Quote } from '../lib/api';

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
  comp = mount(Quotes, { target, props: { refreshMs: 30 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

function setValue(el: HTMLInputElement | HTMLTextAreaElement, v: string) {
  el.value = v;
  el.dispatchEvent(new Event('input', { bubbles: true }));
}

const now = new Date().toISOString();

function engine(initial: Quote[], opts: { workflows?: string[]; refuseTurnOn?: string } = {}) {
  const quotes = [...initial];
  let next = Math.max(0, ...quotes.map((q) => q.id)) + 1;
  const wfs = (opts.workflows ?? []).map((id) => ({ id, name: id, enabled: true, version: 1, updated_at: now, triggers: [] }));
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/quotes': (method, body, query) => {
      if (method === 'POST') {
        const b = body as { text: string; added_by: string; category: string };
        const q: Quote = { id: next++, text: b.text, added_by: b.added_by, category: b.category, created_at: now, updated_at: now };
        quotes.push(q);
        return { status: 201, body: q };
      }
      const find = new URLSearchParams(query).get('q')?.toLowerCase() ?? '';
      const list = quotes.filter((q) => !find || q.text.toLowerCase().includes(find)).sort((a, b) => b.id - a.id);
      return { status: 200, body: { quotes: list, count: quotes.length, next } };
    },
    '/v1/workflows': () => ({ status: 200, body: { workflows: wfs } }),
    '/v1/workflows?deleted=true': () => ({ status: 200, body: { workflows: [] } }),
    '/v1/templates': () => ({
      status: 200,
      body: {
        categories: ['Quotes'],
        templates: ['quote', 'quoteadd', 'quotedel', 'lurk'].map((id) => ({
          id, title: id, category: id === 'lurk' ? 'Chat commands' : 'Quotes', description: '', starter: false, blanks: [],
          workflow: { schema: 'streamgremlin.workflow/v1', id: `chat.${id}`, name: id, triggers: [], steps: [] },
        })),
      },
    }),
  };
  for (let id = 1; id <= 5; id++) {
    routes[`/v1/quotes/${id}`] = (method, body) => {
      const i = quotes.findIndex((q) => q.id === id);
      if (method === 'DELETE') {
        quotes.splice(i, 1);
        return { status: 204, body: null };
      }
      quotes[i] = { ...quotes[i], ...(body as object) };
      return { status: 200, body: quotes[i] };
    };
  }
  const calls = mockFetch(routes);
  // Saving and turning on workflows: answered by method and path.
  const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
  vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) => {
    const method = i?.method ?? 'GET';
    if (method === 'PUT' && p.startsWith('/v1/workflows/')) {
      const body = JSON.parse(String(i!.body));
      calls.push({ method, path: p, body });
      if (p.endsWith('/enabled')) {
        if (opts.refuseTurnOn && p.includes(opts.refuseTurnOn)) {
          return new Response(JSON.stringify({ error: { code: 'command_taken', message: '!quote is already answered by “Old quotes”' } }), { status: 409 });
        }
        return new Response(JSON.stringify({ id: p.split('/')[3], enabled: true }), { status: 200 });
      }
      return new Response(JSON.stringify({ workflow: { id: body.id, name: body.name, enabled: false, version: 1, triggers: [] }, changed: true }), { status: 201 });
    }
    return inner(p, i);
  }));
  return { calls, quotes };
}

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

const q1: Quote = { id: 1, text: 'I meant to do that', added_by: 'Fan', category: 'Celeste', created_at: now, updated_at: now };
const q3: Quote = { id: 3, text: 'chat is always right', added_by: 'you', created_at: now, updated_at: now };

describe('Quotes', () => {
  it('lists quotes by number, newest first, and finds them, accessibly', async () => {
    engine([q1, q3], { workflows: ['chat.quote'] });
    const el = start();
    await settle();
    const rows = Array.from(el.querySelectorAll('ul[aria-label="Quotes"] li')).map((li) => li.textContent);
    expect(rows[0]).toContain('#3 chat is always right');
    expect(rows[1]).toContain('#1 I meant to do that');
    expect(rows[1]).toContain('added by Fan, Celeste');
    expect(el.textContent).toContain('2 quotes');
    expect(el.textContent).toContain('Quote #4'); // the next number, after a gap
    expect(el.textContent).not.toContain('Add the quote commands');
    await noViolations('list');
    setValue(el.querySelector<HTMLInputElement>('input[type="search"]')!, 'chat');
    await settle();
    expect(el.querySelectorAll('ul[aria-label="Quotes"] li')).toHaveLength(1);
  });

  it('adds a quote once, even when the add is repeated', async () => {
    const { calls } = engine([]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('No quotes yet.');
    const form = el.querySelectorAll('form')[0];
    setValue(form.querySelector('textarea')!, 'Never again');
    flushSync();
    button(form, 'Add')!.click();
    await settle();
    expect(el.textContent).toContain('Added quote #1.');
    const post = calls.find((c) => c.method === 'POST' && c.path === '/v1/quotes')!;
    expect(post.body).toMatchObject({ text: 'Never again', added_by: 'you', category: '' });
    expect((post.body as { request_id: string }).request_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(el.textContent).toContain('#1 Never again');
  });

  it('edits and deletes, asking first', async () => {
    const { calls } = engine([q1, q3]);
    const el = start();
    await settle();
    button(el, 'Edit')!.click(); // #3, the first row
    flushSync();
    const form = el.querySelector('ul form')!;
    setValue(form.querySelector('textarea')!, 'chat is usually right');
    button(form, 'Save')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ path: '/v1/quotes/3', body: { text: 'chat is usually right', added_by: 'you', category: '' } });
    expect(el.textContent).toContain('#3 chat is usually right');
    button(el, 'Delete')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Later quotes keep their numbers, and #3 isn’t used again.');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(el.textContent).toContain('Deleted quote #3.');
    expect(el.textContent).not.toContain('usually right');
  });

  it('adds the quote commands from the templates, saying what was refused', async () => {
    const { calls } = engine([], { refuseTurnOn: 'chat.quotedel' });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Nothing answers !quote yet.');
    await noViolations('commands');
    button(el, 'Add the quote commands')!.click();
    await settle();
    const saved = calls.filter((c) => c.method === 'PUT' && c.path.startsWith('/v1/workflows/') && !c.path.endsWith('/enabled'));
    expect(saved.map((c) => c.path)).toEqual(['/v1/workflows/chat.quote', '/v1/workflows/chat.quoteadd', '/v1/workflows/chat.quotedel']);
    expect(calls.filter((c) => c.path.endsWith('/enabled'))).toHaveLength(3);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('!quote is already answered by “Old quotes”');
  });
});
