import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import BackupsCard from './BackupsCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { Backups } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;
let reloads = 0;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  reloads = 0;
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(BackupsCard, { target, props: { reload: () => reloads++ } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text || b.getAttribute('aria-label')?.startsWith(text));

const at = '2026-11-08T12:00:00.000Z';
const name = 'streamgremlin-manual-20261108T120000.000Z.zip';

function engine(over: Partial<Backups> = {}, restore: 'ok' | 'refused' = 'ok') {
  const state: Backups = {
    backups: [
      { name, kind: 'manual', created_at: at, size: 2_500_000, app_version: 'dev' },
      { name: '20261101T000000.000Z-before-migration-0007.db', kind: 'pre-migration', created_at: at, size: 900 },
    ],
    settings: { backup_every_hours: 24, backup_keep: 7, events_days: 30, runs_days: 90 },
    status: { last_backup_at: at, next_backup_at: at },
    ...over,
  };
  const calls = mockFetch({
    '/v1/backups': (method) => {
      if (method === 'POST') return { status: 201, body: { name: 'streamgremlin-manual-new.zip', kind: 'manual', created_at: at, size: 1 } };
      return { status: 200, body: state };
    },
    [`/v1/backups/${name}/verify`]: () => ({ status: 200, body: { ok: true } }),
    [`/v1/backups/${name}/restore`]: (method) => {
      if (method === 'DELETE') {
        state.pending = undefined;
        return { status: 204, body: null };
      }
      if (restore === 'refused') return { status: 400, body: { error: { code: 'invalid_request', message: 'the database is damaged' } } };
      state.pending = name;
      return { status: 202, body: { pending: name, restart_required: true } };
    },
    [`/v1/backups/${name}`]: () => ({ status: 204, body: null }),
    '/desktop/restart-engine': () => ({ status: 202, body: {} }),
    '/v1/settings/maintenance': (_m, body) => ({ status: 200, body }),
    '/v1/maintenance/sweep': () => ({ status: 200, body: { at, events: 12, runs: 3 } }),
  });
  return calls;
}

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

describe('Backups', () => {
  it('lists backups in words, backs up now and checks one', async () => {
    const calls = engine();
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('Sign-ins and passwords are never in a backup');
    expect(el.textContent).toContain('Made by you');
    expect(el.textContent).toContain('Before an update (database only)');
    expect(el.textContent).toContain('2.5 MB');
    button(el, 'Back up now')!.click();
    await settle(40);
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/backups')).toBe(true);
    expect(el.textContent).toContain('Backed up: streamgremlin-manual-new.zip.');
    button(el, 'Check the backup')!.click();
    await settle(40);
    expect(el.textContent).toContain('is good: every file matches');
    await noViolations('backups');
  });

  it('restores only after asking, then restarts the engine', async () => {
    const calls = engine();
    const el = start();
    await settle(40);
    button(el, 'Restore the backup')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('What you have now is backed up first');
    expect(dialog.textContent).toContain('Sign-ins and passwords aren’t changed');
    await noViolations('restore dialog');
    button(dialog, 'Restore and restart')!.click();
    await settle(60);
    const order = calls.filter((c) => c.method === 'POST').map((c) => c.path);
    expect(order).toEqual([`/v1/backups/${name}/restore`, '/desktop/restart-engine']);
    expect(reloads).toBe(1);
  });

  it('says why a backup can’t be restored, and doesn’t restart', async () => {
    const calls = engine({}, 'refused');
    const el = start();
    await settle(40);
    button(el, 'Restore the backup')!.click();
    flushSync();
    button(document.querySelector('[role="alertdialog"]')!, 'Restore and restart')!.click();
    await settle(60);
    expect(el.querySelector('[role="alert"]')!.textContent).toContain('It can’t be restored: the database is damaged');
    expect(calls.some((c) => c.path === '/desktop/restart-engine')).toBe(false);
    expect(reloads).toBe(0);
  });

  it('shows a waiting restore and the last one, and can cancel', async () => {
    const calls = engine({ pending: name, last_restore: { at, from: 'old.zip', ok: false, error: 'it’s from a newer version' } });
    const el = start();
    await settle(40);
    expect(el.textContent).toContain(`${name} will be restored when StreamGremlin next starts.`);
    expect(el.textContent).toContain('didn’t happen, and nothing was changed: it’s from a newer version');
    expect(button(el, 'Delete the backup')!.disabled).toBe(true); // the one waiting
    button(el, 'Cancel the restore')!.click();
    await settle(40);
    expect(calls.some((c) => c.method === 'DELETE' && c.path === `/v1/backups/${name}/restore`)).toBe(true);
    expect(el.textContent).toContain('Cancelled: nothing will be restored.');
  });

  it('saves the schedule and history settings, and clears old history', async () => {
    const calls = engine();
    const el = start();
    await settle(40);
    const every = el.querySelector<HTMLSelectElement>('section[aria-labelledby="history-heading"] select')!;
    every.value = '168';
    every.dispatchEvent(new Event('change', { bubbles: true }));
    const days = el.querySelectorAll<HTMLInputElement>('section[aria-labelledby="history-heading"] input[type="number"]');
    days[1].value = '7';
    days[1].dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Save')!.click();
    await settle(40);
    expect(calls.find((c) => c.method === 'PUT')!.body).toEqual({ backup_every_hours: 168, backup_keep: 7, events_days: 7, runs_days: 90 });
    button(el, 'Clear old history now')!.click();
    await settle(40);
    expect(el.textContent).toContain('Cleared 12 old events and 3 old runs.');
  });

  it('deletes after asking', async () => {
    const calls = engine();
    const el = start();
    await settle(40);
    button(el, 'Delete the backup')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('is deleted for good');
    button(dialog, 'Delete')!.click();
    await settle(40);
    expect(calls.some((c) => c.method === 'DELETE' && c.path === `/v1/backups/${name}`)).toBe(true);
    expect(el.textContent).toContain('Deleted.');
  });
});
