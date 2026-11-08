import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import SavedSecrets from './SavedSecrets.svelte';
import { mockFetch, settle } from '../test/helpers';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('div');
  document.body.appendChild(target);
  comp = mount(SavedSecrets, { target });
  return target;
}

const saved = (key: string) => ({
  key,
  created_at: '2026-11-08T10:00:00.000Z',
  updated_at: '2026-11-08T10:00:00.000Z',
});

/** A fake engine holding two secrets, like the real API: never values. */
function engine(kind: 'keyring' | 'file' = 'keyring') {
  let keys = ['obs/password', 'twitch/42/oauth'];
  const label = kind === 'keyring' ? 'macOS Keychain' : 'an encrypted file in your data folder';
  const listing = (query: string) => ({
    status: 200,
    body: {
      store: { kind, label },
      checked: query === 'check=true',
      secrets: keys.map((k) =>
        query === 'check=true'
          ? k === 'obs/password'
            ? { ...saved(k), state: 'ok' }
            : { ...saved(k), state: 'missing', detail: 'macOS Keychain no longer has it.' }
          : saved(k),
      ),
    },
  });
  const calls = mockFetch({
    '/v1/secrets': (_m, _b, q) => listing(q),
    '/v1/secrets/twitch/42/oauth': (method) => {
      if (method !== 'DELETE') return { status: 405, body: {} };
      keys = keys.filter((k) => k !== 'twitch/42/oauth');
      return { status: 204, body: null };
    },
  });
  return calls;
}

const buttons = (el: HTMLElement, text: string) =>
  Array.from(el.querySelectorAll('button')).filter((b) => b.textContent?.trim() === text);

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Passwords and sign-ins', () => {
  it('says where secrets are kept and lists them, without values', async () => {
    engine();
    const el = start();
    await settle();
    expect(el.textContent).toContain('macOS Keychain');
    expect(el.textContent).toContain('obs/password');
    expect(el.textContent).toContain('twitch/42/oauth');
    expect(el.textContent).not.toContain('secrets.enc');
    for (const theme of ['dark', 'light', 'midnight']) {
      document.documentElement.dataset.theme = theme;
      await noViolations(theme);
    }
  });

  it('explains the encrypted file when the keyring is not used', async () => {
    engine('file');
    const el = start();
    await settle();
    expect(el.textContent).toContain('an encrypted file in your data folder');
    expect(el.textContent).toContain('secrets.key');
  });

  it('shows an empty state before anything is connected', async () => {
    mockFetch({
      '/v1/secrets': () => ({
        status: 200,
        body: { store: { kind: 'keyring', label: 'Windows Credential Manager' }, checked: false, secrets: [] },
      }),
    });
    const el = start();
    await settle();
    expect(el.textContent).toContain('Nothing saved yet');
    expect(buttons(el, 'Check they can be read')).toHaveLength(0);
  });

  it('checks each secret and says which cannot be read, in words', async () => {
    const calls = engine();
    const el = start();
    await settle();
    buttons(el, 'Check they can be read')[0].click();
    await settle();
    expect(calls.some((c) => c.path === '/v1/secrets?check=true')).toBe(true);
    expect(el.textContent).toContain('Can be read');
    expect(el.textContent).toContain('Missing: macOS Keychain no longer has it.');
    expect(el.querySelector('[role="status"]')?.textContent).toBe("Checked: 1 can't be read.");
  });

  it('asks in the app before forgetting, and Cancel keeps it', async () => {
    const calls = engine();
    const el = start();
    await settle();
    const confirmSpy = vi.fn();
    vi.stubGlobal('confirm', confirmSpy);
    const forgetBtn = el.querySelector<HTMLButtonElement>('button[aria-label="Forget twitch/42/oauth"]')!;
    forgetBtn.focus();
    forgetBtn.click();
    flushSync();
    await settle();
    const dialog = document.querySelector('[role="alertdialog"]');
    expect(dialog?.textContent).toContain('Forget twitch/42/oauth?');
    expect(document.activeElement?.textContent).toBe('Cancel');
    await noViolations('dialog');
    dialog!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    await settle();
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(document.activeElement).toBe(forgetBtn);
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);
    expect(confirmSpy).not.toHaveBeenCalled();
  });

  it('forgets after confirming, and the list updates', async () => {
    const calls = engine();
    const el = start();
    await settle();
    el.querySelector<HTMLButtonElement>('button[aria-label="Forget twitch/42/oauth"]')!.click();
    flushSync();
    await settle();
    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    buttons(dialog, 'Forget')[0].click();
    flushSync();
    await settle();
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/secrets/twitch/42/oauth')).toBe(true);
    const listed = Array.from(el.querySelectorAll('.secret-row code')).map((c) => c.textContent);
    expect(listed).toEqual(['obs/password']);
    expect(el.querySelector('[role="status"]')?.textContent).toBe('Forgot twitch/42/oauth.');
  });

  it('says so when forgetting fails', async () => {
    mockFetch({
      '/v1/secrets': () => ({
        status: 200,
        body: { store: { kind: 'keyring', label: 'macOS Keychain' }, checked: false, secrets: [saved('obs/password')] },
      }),
      '/v1/secrets/obs/password': () => ({
        status: 503,
        body: { error: { code: 'unavailable', message: 'macOS Keychain didn’t answer' } },
      }),
    });
    const el = start();
    await settle();
    el.querySelector<HTMLButtonElement>('button[aria-label="Forget obs/password"]')!.click();
    flushSync();
    await settle();
    buttons(document.querySelector<HTMLElement>('[role="alertdialog"]')!, 'Forget')[0].click();
    flushSync();
    await settle();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain("Couldn't forget obs/password");
    expect(el.textContent).toContain('obs/password');
  });
});
