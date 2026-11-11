import axe from 'axe-core';
import { mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Addons from './Addons.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { InstalledPackage, PackageAuditEntry, PackagePreview } from '../lib/api';

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
  comp = mount(Addons, { target });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const now = new Date().toISOString();
const digest = 'ab'.repeat(32);
const paper: InstalledPackage = {
  id: 'paper-wheel', kind: 'theme', name: 'Paper wheel', version: '1.0.0', publisher: 'Someone', description: 'A hand-cut wheel.',
  license: 'MIT', homepage: '', digest, size: 12_000, files: 5, installed_at: now, updated_at: now, used_by: ['wheel'],
  settings: [{ key: 'paper', label: 'Paper', kind: 'choice', default: 'Cream', options: ['Cream', 'Sky'] }],
};

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

function engine(initial: InstalledPackage[], opts: { refuse?: string } = {}) {
  const list = [...initial];
  const audit: PackageAuditEntry[] = [];
  const calls = mockFetch({
    '/v1/packages': () => ({ status: 200, body: { packages: list } }),
    '/v1/packages/audit': () => ({ status: 200, body: { audit } }),
    '/v1/packages/paper-wheel': () => {
      const p = list.splice(0, 1)[0];
      audit.unshift({ seq: 2, at: now, package_id: p.id, version: p.version, action: 'remove', detail: '' });
      return { status: 200, body: { package: p, overlays: p.used_by } };
    },
  });
  // A ZIP goes as bytes, not JSON: answered here.
  const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
  vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) => {
    if (i?.method === 'POST' && p === '/v1/packages/check') {
      calls.push({ method: 'POST', path: p, body: i.body });
      if (opts.refuse) return new Response(JSON.stringify({ error: { code: 'invalid_request', message: opts.refuse } }), { status: 400 });
      const pv: PackagePreview = { ...paper, used_by: undefined, same: list.length > 0, sounds: true } as unknown as PackagePreview;
      return new Response(JSON.stringify(pv), { status: 200 });
    }
    if (i?.method === 'POST' && p.startsWith('/v1/packages?digest=')) {
      calls.push({ method: 'POST', path: p, body: i.body });
      list.push({ ...paper, used_by: [] });
      audit.unshift({ seq: 1, at: now, package_id: 'paper-wheel', version: '1.0.0', action: 'install', detail: '' });
      return new Response(JSON.stringify(paper), { status: 201 });
    }
    return inner(p, i);
  }));
  return { calls, list };
}

function pickFile(el: HTMLElement, name = 'paper-wheel.zip', size = 10) {
  const input = el.querySelector<HTMLInputElement>('input[type="file"]')!;
  Object.defineProperty(input, 'files', { value: [new File([new Uint8Array(size)], name, { type: 'application/zip' })], configurable: true });
  input.dispatchEvent(new Event('change', { bubbles: true }));
}

describe('Add-ons', () => {
  it('checks a ZIP, shows what it is and what it can do, and installs it only when asked', async () => {
    const { calls } = engine([]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('Nothing installed yet');
    pickFile(el);
    await settle();
    const shown = el.querySelector('[aria-label="What it is"]')!.textContent!;
    expect(shown).toContain('Paper wheel 1.0.0');
    expect(shown).toContain('published by Someone (not verified)');
    expect(shown).toContain('can’t reach the internet');
    expect(shown).toContain('can’t check how it draws the odds');
    expect(shown).toContain('It has its own sounds.');
    expect(shown).toContain('Settings: Paper.');
    expect(calls.some((c) => c.path.startsWith('/v1/packages?'))).toBe(false); // nothing installed by looking
    await noViolations('a preview');
    button(el, 'Install Paper wheel')!.click();
    await settle();
    const install = calls.find((c) => c.path.startsWith('/v1/packages?'))!;
    expect(install.path).toBe(`/v1/packages?digest=${digest}`);
    expect(install.body).toBeInstanceOf(Uint8Array); // the bytes, as they are
    expect(el.querySelector('[role="status"]')!.textContent).toContain('Installed Paper wheel 1.0.0');
    expect(el.querySelector('[aria-label="Installed add-ons"]')!.textContent).toContain('published by Someone (not verified)');
    expect(el.querySelector('[aria-label="What it is"]')).toBeNull();
    expect(el.querySelector('[aria-label="Add-on history"]')!.textContent!.replace(/\s+/g, ' ')).toContain('Installed paper-wheel 1.0.0');
  });

  it('says why a package can’t be installed, and refuses one too big before sending it', async () => {
    const { calls } = engine([], { refuse: '"../evil.js": a file’s path can’t have . or .. as a folder' });
    const el = start();
    await settle();
    pickFile(el, 'evil.zip');
    await settle();
    expect(el.querySelector('[role="alert"]')!.textContent).toContain('evil.zip can’t be installed: "../evil.js"');
    expect(el.querySelector('[aria-label="What it is"]')).toBeNull();
    const before = calls.length;
    pickFile(el, 'huge.zip', 16 * 1024 * 1024 + 1);
    await settle();
    expect(el.querySelector('[role="alert"]')!.textContent).toContain('up to 16 MB');
    expect(calls.slice(before).some((c) => c.method === 'POST')).toBe(false);
  });

  it('removes one after asking, naming the overlays that go back to Classic', async () => {
    const { calls } = engine([paper]);
    const el = start();
    await settle();
    expect(el.textContent).toContain('Used by wheel.');
    button(el, 'Remove')!.click();
    await settle();
    const dialog = el.ownerDocument.querySelector('[role="dialog"], [role="alertdialog"]')!;
    expect(dialog.textContent).toContain('These overlays go back to the Classic look: wheel.');
    await noViolations('the dialog');
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    button(dialog, 'Remove')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'DELETE')!.path).toBe('/v1/packages/paper-wheel');
    expect(el.querySelector('[role="status"]')!.textContent).toBe('Removed Paper wheel. Back to the Classic look: wheel.');
    expect(el.textContent).toContain('Nothing installed yet');
  });

  it('says an installed one is installed already', async () => {
    engine([paper]);
    const el = start();
    await settle();
    pickFile(el);
    await settle();
    expect(el.textContent).toContain('It’s installed already.');
    expect(button(el, 'Install Paper wheel')).toBeUndefined();
  });
});
