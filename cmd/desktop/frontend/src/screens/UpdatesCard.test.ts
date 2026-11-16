import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import UpdatesCard from './UpdatesCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { UpdateRelease, UpdateStatus } from '../lib/api';

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
  comp = mount(UpdatesCard, { target, props: { poll: 10 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim().startsWith(text));

/** Waits up to 5 seconds for root's text to include want. */
async function sees(root: Element, want: string) {
  const deadline = Date.now() + 5000;
  while (!root.textContent?.includes(want)) {
    if (Date.now() > deadline) throw new Error(`never saw “${want}” in: ${root.textContent}`);
    await settle(10);
  }
}

const beta6: UpdateRelease = {
  version: '0.1.0-beta.6',
  prerelease: true,
  notes: '## Fixed\n- <b>not bold</b>',
  url: 'https://github.com/shawnbutts/StreamGremlin/releases/tag/v0.1.0-beta.6',
  installable: true,
  file: 'StreamGremlin-0.1.0-beta.6-macos.dmg',
  size: 52_000_000,
};

function engine(over: Partial<UpdateStatus> = {}, install: 'ok' | 'live' = 'ok') {
  const st: UpdateStatus = {
    version: '0.1.0-beta.5',
    settings: { check: true },
    state: 'up_to_date',
    checked_at: '2026-11-16T12:00:00Z',
    download: { state: 'none', done: 0, total: 0 },
    ...over,
  };
  let polls = 0;
  const calls = mockFetch({
    '/v1/updates': () => {
      // A download finishes after a couple of looks.
      if (st.download.state === 'downloading' && ++polls > 2) st.download = { state: 'ready', version: st.latest!.version, done: 52_000_000, total: 52_000_000 };
      return { status: 200, body: st };
    },
    '/v1/updates/check': () => {
      st.state = 'available';
      st.latest = beta6;
      return { status: 200, body: st };
    },
    '/v1/updates/download': () => {
      st.download = { state: 'downloading', version: st.latest!.version, done: 1_000_000, total: 52_000_000 };
      return { status: 202, body: st };
    },
    '/v1/settings/updates': (_m, body) => {
      st.settings = body as { check: boolean };
      return { status: 200, body };
    },
    '/desktop/install-update': () =>
      install === 'live'
        ? { status: 409, body: { error: { code: 'conflict', message: 'you’re live: install the update after your stream' } } }
        : { status: 202, body: {} },
    '/desktop/open-url': () => ({ status: 204, body: null }),
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

describe('Updates', () => {
  it('says it’s up to date, checks now and finds a beta', async () => {
    const calls = engine();
    const el = start();
    await sees(el, 'This is StreamGremlin 0.1.0-beta.5. This is the newest version.');
    expect(el.textContent).toContain('GitHub sees your computer’s internet address');
    await noViolations('up to date');
    button(el, 'Check now')!.click();
    await sees(el, 'StreamGremlin 0.1.0-beta.6 is out.');
    expect(el.textContent).toContain('It’s a beta.');
    // Notes are text: nothing in them becomes markup.
    expect(el.querySelector('.release-notes')!.textContent).toContain('<b>not bold</b>');
    expect(el.querySelector('.release-notes b')).toBeNull();
    expect(calls.filter((c) => c.method === 'POST').map((c) => c.path)).toEqual(['/v1/updates/check']);
    await noViolations('available');
  });

  it('downloads, then installs only after asking', async () => {
    const calls = engine({ state: 'available', latest: beta6 });
    const el = start();
    await sees(el, 'is out');
    button(el, 'Download 0.1.0-beta.6 (52.0 MB)')!.click();
    await sees(el, 'Downloaded, and its signature checks out.');
    expect(calls.some((c) => c.path === '/desktop/install-update')).toBe(false);
    button(el, 'Install and restart')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('backs up your data, closes, installs the update and opens again');
    expect(dialog.textContent).toContain('when you’re not live');
    await noViolations('install dialog');
    button(dialog, 'Install and restart')!.click();
    await sees(el, 'Installing: StreamGremlin closes now');
    expect(calls.filter((c) => c.method === 'POST').map((c) => c.path)).toEqual(['/v1/updates/download', '/desktop/install-update']);
  });

  it('says why it won’t install while you’re live', async () => {
    engine({ state: 'available', latest: beta6, download: { state: 'ready', version: '0.1.0-beta.6', done: 1, total: 1 } }, 'live');
    const el = start();
    await sees(el, 'Downloaded');
    button(el, 'Install and restart')!.click();
    flushSync();
    button(document.querySelector('[role="alertdialog"]')!, 'Install and restart')!.click();
    await sees(el, 'install the update after your stream');
    expect(button(el, 'Install and restart')!.disabled).toBe(false);
  });

  it('points to the release page when the app can’t install it', async () => {
    const calls = engine({
      state: 'available',
      latest: { ...beta6, installable: false, why: 'On Linux, install the .deb, .rpm or .tar.gz from the release page.' },
    });
    const el = start();
    await sees(el, 'On Linux, install the .deb');
    expect(button(el, 'Download')).toBeUndefined();
    button(el, 'Open the release page')!.click();
    await settle(30);
    expect(calls.find((c) => c.path === '/desktop/open-url')!.body).toEqual({ url: beta6.url });
  });

  it('shows a failed download and a failed check in words', async () => {
    engine({ state: 'failed', error: 'couldn’t reach GitHub', latest: undefined });
    let el = start();
    await sees(el, 'The last check didn’t work: couldn’t reach GitHub');
    unmount(comp!);
    document.body.innerHTML = '';
    engine({ state: 'available', latest: beta6, download: { state: 'failed', version: '0.1.0-beta.6', done: 0, total: 1, error: 'the release’s signature doesn’t check out' } });
    el = start();
    await sees(el, 'nothing was installed: the release’s signature doesn’t check out');
    expect(button(el, 'Download 0.1.0-beta.6')).toBeDefined();
  });

  it('turns checking off and on', async () => {
    const calls = engine();
    const el = start();
    await sees(el, 'newest version');
    const box = el.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(box.checked).toBe(true);
    box.click();
    await sees(el, 'Turned off. You can still check now.');
    expect(calls.find((c) => c.path === '/v1/settings/updates')!.body).toEqual({ check: false });
  });

  it('says a development build doesn’t check, with no switch', async () => {
    engine({ version: 'dev', state: 'dev', checked_at: undefined });
    const el = start();
    await sees(el, 'development build');
    expect(el.querySelector('input[type="checkbox"]')).toBeNull();
    expect(button(el, 'Check now')).toBeUndefined();
  });
});
