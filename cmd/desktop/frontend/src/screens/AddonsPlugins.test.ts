import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Addons from './Addons.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { InstalledPackage, PackageAuditEntry, PackagePreview, PluginActivity } from '../lib/api';

// Lua plugins on Add-ons (docs/adr/0038-lua-plugins.md).

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
const checkbox = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.includes(text))?.querySelector<HTMLInputElement>('input[type=checkbox]');
const now = new Date().toISOString();
const digest = 'cd'.repeat(32);

const dice: InstalledPackage = {
  id: 'dice-tray', kind: 'lua', name: 'Dice tray', version: '1.0.0', publisher: 'StreamGremlin examples', description: 'Rolls dice.',
  license: 'MIT', homepage: '', digest, size: 3_000, files: 3, installed_at: now, updated_at: now, used_by: [], kept: [], workflows: ['Roll'],
  settings: [
    { key: 'sides', label: 'Sides on each die', kind: 'number', default: 6, min: 2, max: 100 },
    { key: 'cheer', label: 'Cheer a new best roll in chat', kind: 'switch', default: true },
  ],
  plugin: {
    enabled: false, timeout: 5, script: 'plugin.lua', values: { sides: 6, cheer: true },
    templates: [{ id: 'roll', title: '!roll: roll dice in chat' }],
    events: [{ name: 'round_ended', type: 'plugin.dice_tray.round_ended', label: 'A dice round ends', description: 'A round closed.' }],
    permissions: [{ name: 'chat.send', what: 'Send messages in your chat, as the bot when one is signed in (at most 20 a minute)' }],
    actions: [
      { name: 'roll', label: 'Roll dice', description: 'Rolls dice.' },
      { name: 'best', label: 'The session’s best roll', description: 'Who rolled best.' },
    ],
  },
};

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

function engine(initial: InstalledPackage[], opts: { loadError?: string } = {}) {
  const list = [...initial];
  const audit: PackageAuditEntry[] = [];
  const activity: PluginActivity[] = [{ seq: 1, at: now, version: '1.0.0', call: 'chat.send', outcome: 'refused', detail: 'Fan rolled a 4 (too many)' }];
  const calls = mockFetch({
    '/v1/packages': () => ({ status: 200, body: { packages: list } }),
    '/v1/packages/audit': () => ({ status: 200, body: { audit } }),
    '/v1/packages/dice-tray/enabled': (_m, body) => {
      const on = (body as { enabled: boolean }).enabled;
      if (on && opts.loadError) {
        list[0] = { ...list[0], plugin: { ...list[0].plugin!, enabled: false, error: `it didn't load: ${opts.loadError}` } };
        return { status: 409, body: { error: { code: 'conflict', message: `it didn't load: ${opts.loadError}` } } };
      }
      list[0] = { ...list[0], plugin: { ...list[0].plugin!, enabled: on, error: undefined } };
      return { status: 200, body: list[0] };
    },
    '/v1/packages/dice-tray/settings': (_m, body) => {
      const s = (body as { settings: Record<string, unknown> }).settings;
      list[0] = { ...list[0], plugin: { ...list[0].plugin!, values: { ...list[0].plugin!.values, ...s } } };
      return { status: 200, body: list[0] };
    },
    '/v1/packages/dice-tray/activity': () => ({ status: 200, body: { activity } }),
    '/v1/packages/dice-tray': () => {
      const p = list.splice(0, 1)[0];
      return { status: 200, body: { package: p, overlays: [], workflows: p.workflows } };
    },
  });
  const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (p: string, i?: RequestInit) => {
      if (i?.method === 'POST' && p === '/v1/packages/check') {
        const { workflows: _w, used_by: _u, ...rest } = dice;
        const pv = { ...rest, same: false, sounds: false, update: false, older: false, publisher_change: false } as PackagePreview;
        return new Response(JSON.stringify(pv), { status: 200 });
      }
      if (i?.method === 'POST' && p.startsWith('/v1/packages?digest=')) {
        calls.push({ method: 'POST', path: p, body: undefined });
        if (!p.includes('&permissions=chat.send')) {
          return new Response(JSON.stringify({ error: { code: 'confirm_permissions', message: 'approve them' } }), { status: 409 });
        }
        list.push({ ...dice, workflows: [] });
        return new Response(JSON.stringify({ ...dice, overlays: [] }), { status: 201 });
      }
      return inner(p, i);
    }),
  );
  return { calls, list };
}

async function pickZip(target: HTMLElement) {
  const input = target.querySelector<HTMLInputElement>('#package-file')!;
  const f = new File([new Uint8Array([80, 75, 3, 4])], 'dice-tray.zip', { type: 'application/zip' });
  Object.defineProperty(input, 'files', { value: [f] });
  input.dispatchEvent(new Event('change', { bubbles: true }));
  await settle();
}

describe('plugins on Add-ons', () => {
  it('installs only with each permission ticked, and says it’s off', async () => {
    const { calls } = engine([]);
    const target = start();
    await settle();
    await pickZip(target);
    const preview = target.querySelector('[aria-label="What it is"]')!;
    expect(preview.textContent).toContain('A Lua plugin, published by StreamGremlin examples (not verified)');
    expect(preview.textContent).toContain('It can’t reach your files, the internet or your accounts');
    expect(preview.textContent).toContain('Roll dice');
    expect(preview.textContent).not.toContain('OBS');
    expect(preview.textContent).toContain('Templates it adds to the gallery, in its own section: !roll: roll dice in chat.');
    expect(preview.textContent!.replace(/\s+/g, ' ')).toContain('Events it sends, to start your workflows: A dice round ends: A round closed.');
    const install = button(target, 'Install Dice tray')!;
    expect(install.disabled).toBe(true);
    await noViolations('preview');
    checkbox(target, 'Send messages in your chat')!.click();
    flushSync();
    expect(install.disabled).toBe(false);
    install.click();
    await settle();
    expect(calls.some((c) => c.path.includes('&permissions=chat.send'))).toBe(true);
    expect(target.textContent).toContain('It’s off: turn it on below');
  });

  it('turns on and off, saves settings, shows its activity, and says why it failed', async () => {
    engine([dice]);
    const target = start();
    await settle();
    const card = target.querySelector('[aria-label="Installed add-ons"] li')!;
    expect(card.textContent).toContain('Lua plugin · published by StreamGremlin examples (not verified)');
    expect(card.textContent).toContain('Send messages in your chat');
    expect(card.textContent).toContain('Used by Roll.');
    expect(card.textContent).toContain('Find them in the gallery under “Dice tray (add-on)”');
    expect(card.textContent).toContain('Events it sends: A dice round ends. Find them under “When” in a workflow, under “Dice tray (add-on)”.');
    expect(card.textContent).toContain('Off. Its steps fail until you turn it on.');
    await noViolations('installed');

    const on = card.querySelector<HTMLInputElement>('input[role=switch]')!;
    on.click();
    await settle();
    expect(target.textContent).toContain('Dice tray is on.');
    expect(target.textContent).toContain('It keeps its own memory while it’s on');

    const save = button(target, 'Save settings')!;
    expect(save.disabled).toBe(true);
    const sides = Array.from(target.querySelectorAll('label')).find((l) => l.textContent?.includes('Sides on each die'))!.querySelector('input')!;
    sides.value = '20';
    sides.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    expect(save.disabled).toBe(false);
    save.click();
    await settle();
    expect(target.textContent).toContain('started again, fresh');

    button(target, 'Show what it has done')!.click();
    await settle();
    const list = target.querySelector('[aria-label="What Dice tray has done"]')!;
    expect(list.textContent).toContain('Chat');
    expect(list.textContent).toContain('(refused)');
    await noViolations('activity');

    target.querySelector<HTMLInputElement>('input[role=switch]')!.click();
    await settle();
    expect(target.textContent).toContain('its workflows stay on');
  });

  it('says why a plugin didn’t load', async () => {
    engine([dice], { loadError: 'line 3: syntax error' });
    const target = start();
    await settle();
    target.querySelector<HTMLInputElement>('input[role=switch]')!.click();
    await settle();
    expect(target.querySelector('[role=alert]')?.textContent).toContain('line 3: syntax error');
    expect(target.textContent).toContain("Off: it didn't load: line 3: syntax error");
  });

  it('names the workflows turned off when it’s removed', async () => {
    engine([dice]);
    const target = start();
    await settle();
    button(target, 'Remove')!.click();
    await settle();
    const dialog = document.querySelector('[role="dialog"], [role="alertdialog"]')!;
    expect(dialog.textContent).toContain('These workflows use its steps and are turned off, not deleted: Roll.');
    Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Remove')!.click();
    await settle();
    expect(target.textContent).toContain('Turned off (not deleted): Roll.');
  });

  it('updates asking only for what’s new, naming the workflows turned off, and rolls back', async () => {
    const installed: InstalledPackage = { ...dice, version: '1.0.0', kept: [], plugin: { ...dice.plugin!, enabled: true, permissions: [] } };
    const list = [installed];
    const calls = mockFetch({
      '/v1/packages': () => ({ status: 200, body: { packages: list } }),
      '/v1/packages/audit': () => ({ status: 200, body: { audit: [] } }),
      '/v1/packages/dice-tray/rollback': (_m, _b, query) => {
        list[0] = { ...installed, kept: ['1.2.0'] };
        return { status: 200, body: { ...list[0], from: '1.2.0', overlays: [], workflows: [], reset: [], query } };
      },
    });
    const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (p: string, i?: RequestInit) => {
        if (i?.method === 'POST' && p === '/v1/packages/check') {
          const { workflows: _w, used_by: _u, ...rest } = dice;
          const pv: PackagePreview = {
            ...rest, version: '1.2.0', installed, same: false, sounds: false, update: true, older: false, publisher_change: false,
            plugin_update: {
              new_permissions: [dice.plugin!.permissions[0]], permissions: ['chat.send'],
              removed_steps: [{ name: 'roll', label: 'Roll dice', description: 'Rolls dice.' }], reset: ['Sides on each die'], workflows: ['Throw'],
            },
          };
          return new Response(JSON.stringify(pv), { status: 200 });
        }
        if (i?.method === 'POST' && p.startsWith('/v1/packages?digest=')) {
          calls.push({ method: 'POST', path: p, body: undefined });
          const now = { ...dice, version: '1.2.0', kept: ['1.0.0'], rollback_to: '1.0.0', workflows: ['Roll'],
            plugin: { ...dice.plugin!, enabled: true, rollback: {
              new_permissions: [], permissions: [], removed_steps: [], reset: [], workflows: [] } } };
          list[0] = now;
          return new Response(JSON.stringify({ ...now, from: '1.0.0', overlays: [], workflows: ['Throw'], reset: ['Sides on each die'] }), { status: 201 });
        }
        return inner(p, i);
      }),
    );
    const target = start();
    await settle();
    await pickZip(target);
    const preview = target.querySelector('[aria-label="What it is"]')!;
    expect(preview.textContent).toContain('Dice tray 1.0.0 → 1.2.0');
    expect(preview.textContent).toContain('If it’s on, it starts again, fresh');
    const changes = target.querySelector('[aria-label="What updating changes"]')!;
    expect(changes.textContent).toContain('Steps this version doesn’t have: Roll dice.');
    expect(changes.textContent).toContain('These workflows use them and are turned off, not deleted: Throw.');
    expect(changes.textContent).toContain('Back to the default, as this version doesn’t take what’s set: Sides on each die.');
    expect(preview.textContent).toContain('This version asks for more. Tick each one you allow:');
    const update = button(target, 'Update Dice tray')!;
    expect(update.disabled).toBe(true);
    await noViolations('update preview');
    checkbox(target, 'Send messages in your chat')!.click();
    flushSync();
    update.click();
    await settle();
    expect(calls.some((c) => c.path.includes('&permissions=chat.send'))).toBe(true);
    const status = Array.from(target.querySelectorAll('[role=status]')).find((x) => x.textContent?.includes('Updated'))!;
    expect(status.textContent).toContain('Updated Dice tray from 1.0.0 to 1.2.0.');
    expect(status.textContent).toContain('Turned off, as they used a step it doesn’t have (not deleted): Throw.');
    expect(status.textContent).toContain('It started again, fresh, as 1.2.0.');

    button(target, 'Roll back to 1.0.0')!.click();
    await settle();
    const dialog = document.querySelector('[role="dialog"], [role="alertdialog"]')!;
    expect(dialog.textContent).toContain('It starts again, fresh: its memory starts over.');
    Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Roll back')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/packages/dice-tray/rollback')).toBe(true);
    expect(target.textContent).toContain('Rolled Dice tray back from 1.2.0 to 1.0.0.');
  });

  it('asks before rolling back to a version that asks for more', async () => {
    const p: InstalledPackage = { ...dice, version: '1.3.0', rollback_to: '1.2.0', kept: ['1.2.0'], plugin: { ...dice.plugin!, permissions: [],
      rollback: { new_permissions: [dice.plugin!.permissions[0]], permissions: ['chat.send'],
        removed_steps: [{ name: 'best', label: 'The session’s best roll', description: '' }], reset: [], workflows: ['Best'] } } };
    const calls = mockFetch({
      '/v1/packages': () => ({ status: 200, body: { packages: [p] } }),
      '/v1/packages/audit': () => ({ status: 200, body: { audit: [] } }),
      '/v1/packages/dice-tray/rollback': () => ({ status: 200, body: { ...p, version: '1.2.0', from: '1.3.0', overlays: [], workflows: ['Best'] } }),
    });
    const target = start();
    await settle();
    button(target, 'Roll back to 1.2.0')!.click();
    await settle();
    const dialog = document.querySelector('[role="dialog"], [role="alertdialog"]')!;
    expect(dialog.textContent).toContain('1.2.0 asks for more: rolling back allows it to send messages in your chat');
    expect(dialog.textContent).toContain('1.2.0 doesn’t have The session’s best roll: these workflows use it and are turned off, not deleted: Best.');
    await noViolations('rollback dialog');
    Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Allow and roll back')!.click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/packages/dice-tray/rollback?permissions=chat.send')).toBe(true);
    expect(target.textContent).toContain('Turned off, as they used a step it doesn’t have (not deleted): Best.');
  });
});
