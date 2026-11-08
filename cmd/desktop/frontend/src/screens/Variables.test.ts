import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Variables from './Variables.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Variable } from '../lib/api';

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
  comp = mount(Variables, { target, props: { refreshMs: 30 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

const now = new Date().toISOString();

function engine(initial: Variable[]) {
  const vars = [...initial];
  const calls = mockFetch({
    '/v1/workflows': () => ({
      status: 200,
      body: { workflows: [{ id: 'chat.lurk', name: 'Lurk', enabled: true, version: 1, updated_at: now, triggers: [] }] },
    }),
    '/v1/variables': (method, body, query) => {
      const q = new URLSearchParams(query);
      if (method === 'PUT') {
        const b = body as Variable;
        if (b.name === 'nope') return { status: 400, body: { error: { code: 'invalid_request', message: 'the value is too big' } } };
        const v: Variable = { scope: b.scope, owner: b.owner, name: b.name, value: b.value, updated_at: now, updated_by: 'you' };
        const i = vars.findIndex((x) => x.scope === v.scope && x.owner === v.owner && x.name === v.name);
        if (i >= 0) vars[i] = { ...vars[i], ...v };
        else vars.push(v);
        return { status: 200, body: v };
      }
      if (method === 'DELETE') {
        const i = vars.findIndex((x) => x.scope === q.get('scope') && x.owner === q.get('owner') && x.name === q.get('name'));
        vars.splice(i, 1);
        return { status: 204, body: null };
      }
      const scope = q.get('scope');
      const find = (q.get('q') ?? '').toLowerCase();
      return {
        status: 200,
        body: {
          variables: vars.filter(
            (v) => (!scope || v.scope === scope) && (!find || v.name.includes(find) || (v.owner_label ?? '').toLowerCase().includes(find)),
          ),
        },
      };
    },
  });
  return { calls, vars };
}

const deaths: Variable = { scope: 'global', owner: '', name: 'deaths', value: 3, updated_at: now, updated_by: 'workflow:chat.deaths' };
const lurks: Variable = { scope: 'viewer', owner: 'twitch:42', owner_label: 'Fan', name: 'lurks', value: 2, updated_at: now, updated_by: 'workflow:chat.lurk' };
const last: Variable = { scope: 'workflow', owner: 'chat.lurk', name: 'last', value: { who: 'fan' }, updated_at: now, updated_by: 'you' };

async function noViolations() {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), theme).toEqual([]);
  }
}

function setValue(el: HTMLInputElement | HTMLSelectElement, value: string) {
  el.value = value;
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
  flushSync();
}

describe('Variables', () => {
  it('says what saved variables are when there are none', async () => {
    engine([]);
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Nothing saved yet');
    expect(el.textContent).toContain('saved.global.deaths');
    await noViolations();
  });

  it('lists every scope, filters, and says who changed each', async () => {
    const e = engine([deaths, lurks, last]);
    const el = start();
    await settle(40);
    const rows = el.querySelectorAll('[aria-label="Variables"] li');
    expect(rows).toHaveLength(3);
    expect(el.textContent).toContain('Viewer: Fan');
    expect(el.textContent).toContain('Workflow: Lurk'); // the workflow's name, not its id
    expect(el.textContent).toContain('by Lurk');
    expect(el.textContent).toContain('{"who":"fan"}');
    button(el, 'Per viewer')!.click();
    await settle(40);
    expect(e.calls.at(-1)!.path).toContain('scope=viewer');
    expect(el.querySelectorAll('[aria-label="Variables"] li')).toHaveLength(1);
    expect(button(el, 'Per viewer')!.getAttribute('aria-pressed')).toBe('true');
    setValue(el.querySelector<HTMLInputElement>('input[type="search"]')!, 'zzz');
    await settle(40);
    expect(el.textContent).toContain('None match.');
    await noViolations();
  });

  it('changes a value, refusing one that is not a number', async () => {
    const e = engine([deaths]);
    const el = start();
    await settle(40);
    button(el, 'Change')!.click();
    flushSync();
    const form = el.querySelector<HTMLFormElement>('form[aria-label="Change deaths"]')!;
    expect(form.querySelector('select')!.value).toBe('number');
    setValue(form.querySelector('input')!, 'lots');
    button(form, 'Save')!.click();
    await settle(40);
    expect(el.textContent).toContain('Type a number');
    expect(e.calls.some((c) => c.method === 'PUT')).toBe(false);
    setValue(form.querySelector('input')!, '7');
    button(form, 'Save')!.click();
    await settle(60);
    expect(e.calls.find((c) => c.method === 'PUT')!.body).toEqual({ scope: 'global', owner: '', name: 'deaths', value: 7 });
    expect(el.textContent).toContain('Saved deaths.');
    expect(el.querySelector('form[aria-label="Change deaths"]')).toBeNull();
    expect(el.textContent).toContain('7');
  });

  it('adds a workflow variable, checking the name first', async () => {
    const e = engine([]);
    const el = start();
    await settle(40);
    button(el, 'Add a variable')!.click();
    flushSync();
    const section = el.querySelector<HTMLElement>('section[aria-labelledby="add-var-heading"]')!;
    const [where, kind] = section.querySelectorAll('select');
    const [name, value] = section.querySelectorAll('input');
    setValue(where, 'chat.lurk');
    setValue(name, 'Last Seen');
    button(section, 'Save')!.click();
    await settle(30);
    expect(section.textContent).toContain('lowercase letters');
    setValue(name, 'greeting');
    setValue(kind, 'text');
    setValue(value, 'hello there');
    button(section, 'Save')!.click();
    await settle(60);
    expect(e.calls.find((c) => c.method === 'PUT')!.body).toEqual({ scope: 'workflow', owner: 'chat.lurk', name: 'greeting', value: 'hello there' });
    expect(el.querySelector('section[aria-labelledby="add-var-heading"]')).toBeNull();
    expect(el.textContent).toContain('“hello there”');
    await noViolations();
  });

  it('asks before deleting, with the app’s own dialog', async () => {
    const e = engine([lurks]);
    const el = start();
    await settle(40);
    button(el, 'Delete')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Delete lurks?');
    expect(dialog.textContent).toContain('Viewer: Fan');
    await noViolations();
    button(dialog, 'Cancel')!.click();
    flushSync();
    expect(e.calls.some((c) => c.method === 'DELETE')).toBe(false);
    button(el, 'Delete')!.click();
    flushSync();
    button(document.querySelector('[role="alertdialog"]')!, 'Delete')!.click();
    await settle(60);
    expect(e.calls.find((c) => c.method === 'DELETE')!.path).toBe('/v1/variables?scope=viewer&owner=twitch%3A42&name=lurks');
    expect(el.textContent).toContain('Deleted lurks.');
    expect(el.textContent).toContain('Nothing saved yet');
  });
});
