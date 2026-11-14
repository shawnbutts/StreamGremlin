import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import YouTubeCard from './YouTubeCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { YouTubeAccount, YouTubeStatus } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);
const field = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.trim().startsWith(text))!.querySelector('input') as HTMLInputElement;
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

const CLIENT = '123456789012-abcdefghijklmnopqrstuvwxyz012345.apps.googleusercontent.com';

const account = (role: 'streamer' | 'bot', state: YouTubeAccount['state']): YouTubeAccount => ({
  role,
  state,
  scopes: [],
  missing_scopes: [],
  since: new Date().toISOString(),
});

function engine() {
  const st: YouTubeStatus = {
    client_id: '',
    client_source: 'none',
    has_secret: false,
    has_built_in: false,
    streamer: account('streamer', 'not_configured'),
    bot: account('bot', 'not_configured'),
    quota: { used: 0, limit: 10000, day: '2026-11-13', resets_at: new Date().toISOString(), exhausted: false },
  };
  const opened: string[] = [];
  const calls = mockFetch({
    '/v1/connections/youtube': () => ({ status: 200, body: st }),
    '/v1/connections/youtube/client': (_m, body) => {
      const b = body as { client_id: string; client_secret: string };
      if (!b.client_id.endsWith('.apps.googleusercontent.com'))
        return { status: 400, body: { error: { code: 'invalid_request', message: 'a client ID looks like …apps.googleusercontent.com' } } };
      Object.assign(st, { client_id: b.client_id, client_source: 'own', has_secret: b.client_secret !== '' });
      st.streamer = account('streamer', 'signed_out');
      st.bot = account('bot', 'signed_out');
      return { status: 200, body: st };
    },
    '/v1/connections/youtube/streamer/sign-in': (m) => {
      if (m === 'DELETE') {
        st.streamer = account('streamer', 'signed_out');
        return { status: 204, body: null };
      }
      const p = { auth_url: 'https://accounts.google.com/o/oauth2/v2/auth?client_id=x', expires_at: new Date(Date.now() + 600_000).toISOString() };
      st.streamer = { ...account('streamer', 'signing_in'), sign_in: p };
      return { status: 200, body: p };
    },
    '/v1/connections/youtube/streamer/sign-out': () => {
      st.streamer = account('streamer', 'signed_out');
      return { status: 204, body: null };
    },
    '/desktop/open-url': (_m, body) => {
      opened.push((body as { url: string }).url);
      return { status: 204, body: null };
    },
  });
  return { st, calls, opened };
}

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(YouTubeCard, { target, props: { fastMs: 20, slowMs: 20 } });
  return target;
}

describe('YouTube on Connections', () => {
  it('guides making a Google project, then signs in and out', async () => {
    const { st, calls, opened } = engine();
    const el = start();
    await settle(40);
    expect(el.textContent).toContain('uses your own Google project for now');
    expect(el.textContent).toContain('Add your Google project above first.');
    expect(el.querySelector('details.guide summary')?.textContent).toContain('How to make your Google project');
    expect(el.textContent).toContain('Publish app');
    expect(el.textContent).toContain('Desktop app');

    button(el, 'Create a Google Cloud project')!.click();
    await settle();
    expect(opened).toContain('https://console.cloud.google.com/projectcreate');

    typed(field(el, 'Client ID'), 'nope');
    button(el, 'Save')!.click();
    await settle();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('apps.googleusercontent.com');

    typed(field(el, 'Client ID'), CLIENT);
    typed(field(el, 'Client secret'), 'GOCSPX-secret');
    expect(field(el, 'Client secret').type).toBe('password');
    button(el, 'Save')!.click();
    await settle();
    const put = calls.filter((c) => c.path === '/v1/connections/youtube/client').at(-1);
    expect(put?.body).toEqual({ client_id: CLIENT, client_secret: 'GOCSPX-secret' });
    expect(el.textContent).toContain('Using your own Google project');
    expect(el.textContent).toContain('with its client secret');

    button(el, 'Sign in with Google')!.click();
    await settle(60);
    expect(opened).toContain('https://accounts.google.com/o/oauth2/v2/auth?client_id=x');
    expect(el.textContent).toContain('Waiting for you to sign in with Google');

    st.streamer = { ...account('streamer', 'connected'), channel: { id: 'UC1', title: 'Streamer', handle: '@streamer' } };
    st.quota.used = 1;
    await settle(60);
    expect(el.textContent).toContain('Signed in as Streamer (@streamer)');
    expect(el.textContent).toContain('About 1 of 10,000 units used');

    button(el, 'Sign out')!.click();
    await settle();
    expect(document.body.textContent).toContain('tells Google to end it');
    button(document.querySelector('[role="alertdialog"]')!, 'Sign out')!.click();
    await settle(60);
    expect(calls.some((c) => c.path === '/v1/connections/youtube/streamer/sign-out')).toBe(true);
    expect(el.textContent).toContain('Not signed in.');

    const results = await axe.run(el, { rules: { 'color-contrast': { enabled: false } } });
    expect(results.violations).toEqual([]);
  });

  it('says when the quota is used up, and when the engine is unknown', async () => {
    const { st } = engine();
    st.quota.exhausted = true;
    const el = start();
    await settle(40);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Used up: YouTube said so');

    vi.stubGlobal('fetch', () => Promise.reject(new TypeError('Failed to fetch')));
    await settle(60);
    expect(el.textContent).toContain('Unknown');
    expect(el.textContent).not.toContain('Used up');
  });
});
