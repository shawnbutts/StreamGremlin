import axe from 'axe-core';
import { mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Addons from './Addons.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { InstalledPackage, PackageAuditEntry, PackagePreview, VersionAnswer } from '../lib/api';

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
  license: 'MIT', homepage: '', digest, size: 12_000, files: 5, installed_at: now, updated_at: now, used_by: ['wheel'], kept: [],
  settings: [{ key: 'paper', label: 'Paper', kind: 'choice', default: 'Cream', options: ['Cream', 'Sky'] }],
};

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

function engine(
  initial: InstalledPackage[],
  opts: { refuse?: string; preview?: Partial<PackagePreview>; answer?: Partial<VersionAnswer>; publisher?: string } = {},
) {
  const list = [...initial];
  const audit: PackageAuditEntry[] = [];
  const calls = mockFetch({
    '/v1/packages': () => ({ status: 200, body: { packages: list } }),
    '/v1/packages/audit': () => ({ status: 200, body: { audit } }),
    '/v1/packages/paper-wheel/rollback': () => {
      const p = list[0];
      list[0] = { ...p, version: p.rollback_to!, kept: [p.version], rollback_to: undefined };
      return { status: 200, body: { ...list[0], from: p.version, overlays: [{ overlay: 'wheel', reset: ['Paper'] }] } };
    },
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
      const pv = { ...paper, used_by: undefined, same: list.length > 0, sounds: true, ...opts.preview } as unknown as PackagePreview;
      return new Response(JSON.stringify(pv), { status: 200 });
    }
    if (i?.method === 'POST' && p.startsWith('/v1/packages?digest=')) {
      calls.push({ method: 'POST', path: p, body: i.body });
      if (opts.publisher !== undefined && !p.endsWith(`&publisher=${encodeURIComponent(opts.publisher)}`)) {
        return new Response(JSON.stringify({ error: { code: 'confirm_publisher', message: 'confirm' } }), { status: 409 });
      }
      if (opts.answer) {
        list.splice(0, list.length, { ...paper, ...opts.answer } as InstalledPackage);
        return new Response(JSON.stringify({ ...paper, ...opts.answer }), { status: 201 });
      }
      list.push({ ...paper, used_by: [] });
      audit.unshift({ seq: 1, at: now, package_id: 'paper-wheel', version: '1.0.0', action: 'install', detail: '' });
      return new Response(JSON.stringify({ ...paper, overlays: [] }), { status: 201 });
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
    expect(shown.replace(/\s+/g, ' ')).toContain('only loads while the wheel’s browser source has “Page permissions” at “No access to OBS”');
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

  it('updates to another version: says what it replaces, keeps the old one, and names settings reset', async () => {
    const installed = { ...paper, version: '1.0.0' };
    const { calls } = engine([installed], {
      preview: { version: '1.1.0', same: false, update: true, older: false, publisher_change: false, installed },
      answer: { version: '1.1.0', from: '1.0.0', kept: ['1.0.0'], rollback_to: '1.0.0', overlays: [{ overlay: 'wheel', reset: ['Paper'] }] },
    });
    const el = start();
    await settle();
    pickFile(el);
    await settle();
    const shown = el.querySelector('[aria-label="What it is"]')!.textContent!.replace(/\s+/g, ' ');
    expect(shown).toContain('Paper wheel 1.0.0 → 1.1.0');
    expect(shown).toContain('1.0.0 is kept, so you can roll back to it');
    expect(shown).not.toContain('Another publisher');
    button(el, 'Update Paper wheel')!.click();
    await settle();
    expect(calls.find((c) => c.path.startsWith('/v1/packages?'))!.path).toBe(`/v1/packages?digest=${digest}`);
    expect(el.querySelector('[role="status"]')!.textContent).toBe(
      'Updated Paper wheel from 1.0.0 to 1.1.0. wheel uses it now. On wheel, Paper went back to the default: 1.1.0 doesn’t take what was set.',
    );
    expect(el.textContent).toContain('Kept to roll back to: 1.0.0.');
  });

  it('another publisher’s version: only with their name typed, exactly', async () => {
    const installed = { ...paper, version: '1.0.0' };
    const { calls } = engine([installed], {
      publisher: 'Someone Else',
      preview: { version: '1.1.0', publisher: 'Someone Else', same: false, update: true, publisher_change: true, installed },
      answer: { version: '1.1.0', publisher: 'Someone Else', from: '1.0.0', kept: ['1.0.0'], overlays: [] },
    });
    const el = start();
    await settle();
    pickFile(el);
    await settle();
    const group = el.querySelector('[role="group"]')!;
    expect(group.textContent!.replace(/\s+/g, ' ')).toContain('The installed Paper wheel says it’s published by Someone; this one says Someone Else.');
    await noViolations('a publisher change');
    const update = button(el, 'Update Paper wheel')!;
    const input = group.querySelector('input')!;
    for (const wrong of ['', 'someone else', 'Someone']) {
      input.value = wrong;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      await settle(0);
      expect(update.disabled).toBe(true);
    }
    input.value = 'Someone Else';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await settle(0);
    expect(update.disabled).toBe(false);
    update.click();
    await settle();
    expect(calls.find((c) => c.path.startsWith('/v1/packages?'))!.path).toBe(`/v1/packages?digest=${digest}&publisher=Someone%20Else`);
    expect(el.querySelector('[role="status"]')!.textContent).toContain('Updated Paper wheel from 1.0.0 to 1.1.0.');
  });

  it('rolls back to the version before, after asking', async () => {
    const { calls } = engine([{ ...paper, version: '1.1.0', kept: ['1.0.0'], rollback_to: '1.0.0' }]);
    const el = start();
    await settle();
    button(el, 'Roll back to 1.0.0')!.click();
    await settle();
    const dialog = el.ownerDocument.querySelector('[role="dialog"], [role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Roll Paper wheel back to 1.0.0?');
    expect(dialog.textContent).toContain('1.1.0 is kept, so you can update to it again. These overlays switch back with it: wheel.');
    await noViolations('the roll back dialog');
    expect(calls.some((c) => c.path.endsWith('/rollback'))).toBe(false);
    button(dialog, 'Roll back')!.click();
    await settle();
    expect(calls.find((c) => c.path.endsWith('/rollback'))!.method).toBe('POST');
    expect(el.querySelector('[role="status"]')!.textContent).toContain('Rolled Paper wheel back from 1.1.0 to 1.0.0. On wheel, Paper went back');
    expect(button(el, 'Roll back to 1.0.0')).toBeUndefined();
  });
});
