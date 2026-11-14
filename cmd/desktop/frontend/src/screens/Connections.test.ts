import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Connections from './Connections.svelte';
import { mockFetch, settle, twitchStatus } from '../test/helpers';

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
  comp = mount(Connections, { target, props: { fastMs: 10, slowMs: 10 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

/** A fake engine whose Twitch state moves like the real one. */
function engine(initial: Parameters<typeof twitchStatus>[0] = {}) {
  let state = twitchStatus(initial).body.twitch as Record<string, any>;
  const calls = mockFetch({
    '/v1/connections': () => ({ status: 200, body: { twitch: state } }),
    '/v1/connections/twitch/client-id': (_m, body) => {
      const id = (body as { client_id: string }).client_id;
      if (id.length < 10) return { status: 400, body: { error: { code: 'invalid_request', message: 'a Client ID is 10 to 64 lowercase letters' } } };
      state = { ...state, client_id: id, client_id_source: 'pasted', streamer: { ...state.streamer, state: 'signed_out' }, bot: { ...state.bot, state: 'signed_out' } };
      return { status: 200, body: { twitch: state } };
    },
    '/v1/connections/twitch/streamer/sign-in': (method) => {
      if (method === 'DELETE') {
        state = { ...state, streamer: { ...state.streamer, state: 'signed_out', sign_in: undefined } };
        return { status: 204, body: null };
      }
      const p = { user_code: 'WXYZABCD', verification_uri: 'https://www.twitch.tv/activate?public=true&device-code=WXYZABCD', expires_at: new Date(Date.now() + 1800e3).toISOString() };
      state = { ...state, streamer: { ...state.streamer, state: 'signing_in', sign_in: p } };
      return { status: 200, body: p };
    },
    '/v1/connections/twitch/streamer/sign-out': () => {
      state = { ...state, streamer: { ...state.streamer, state: 'signed_out', login: undefined } };
      return { status: 204, body: null };
    },
    '/desktop/open-url': () => ({ status: 204, body: null }),
    '/v1/chat/messages': (_m, body) => {
      const b = body as { text: string; as: string };
      const asBot = b.as !== 'streamer' && state.bot.state === 'connected';
      return { status: 200, body: { twitch: { message_id: 'm1', sent_as: asBot ? 'bot' : 'streamer', login: asBot ? state.bot.login : state.streamer.login } } };
    },
  });
  return {
    calls,
    set: (f: (s: Record<string, any>) => Record<string, any>) => (state = f(state)),
  };
}

describe('Connections', () => {
  it('asks for a Client ID when the build has none, then offers sign-in', async () => {
    const e = engine({ client_id: '', client_id_source: 'none', streamer: { state: 'not_configured' }, bot: { state: 'not_configured' } });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('needs a Twitch app’s Client ID');
    expect(button(el, 'Sign in with Twitch')).toBeUndefined();
    await noViolations('no client id');

    button(el, 'Open the Twitch developer console')!.click();
    await settle(20);
    expect(e.calls.some((c) => c.path === '/desktop/open-url' && (c.body as { url: string }).url === 'https://dev.twitch.tv/console')).toBe(true);

    const input = el.querySelector<HTMLInputElement>('label.field input')!;
    input.value = 'short';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Save Client ID')!.click();
    await settle(30);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('10 to 64');

    input.value = 'abcdefghij0123456789';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Save Client ID')!.click();
    await settle(40);
    expect(el.textContent).toContain('Using your own Twitch app');
    expect(button(el, 'Sign in with Twitch')).toBeDefined();
  });

  it('signs in: shows the code, opens Twitch, then shows the account', async () => {
    const e = engine();
    const el = start();
    await settle(30);
    button(el, 'Sign in with Twitch')!.click();
    await settle(40);
    expect(e.calls.some((c) => c.method === 'POST' && c.path === '/v1/connections/twitch/streamer/sign-in')).toBe(true);
    const opened = e.calls.find((c) => c.path === '/desktop/open-url');
    expect((opened?.body as { url: string }).url).toContain('twitch.tv/activate');
    expect(el.querySelector('.user-code')?.textContent).toBe('WXYZABCD');
    expect(el.textContent).toContain('Waiting for you to enter the code');
    await noViolations('signing in');

    e.set((s) => ({ ...s, streamer: { ...s.streamer, state: 'connected', login: 'gremlinstreamer', sign_in: undefined } }));
    await settle(40);
    expect(el.textContent).toContain('Signed in as gremlinstreamer');
    expect(el.querySelector('.user-code')).toBeNull();
  });

  it('cancels a pending sign-in', async () => {
    const e = engine();
    const el = start();
    await settle(30);
    button(el, 'Sign in with Twitch')!.click();
    await settle(40);
    button(el.querySelector('.code-box')!, 'Cancel')!.click();
    await settle(40);
    expect(e.calls.some((c) => c.method === 'DELETE' && c.path === '/v1/connections/twitch/streamer/sign-in')).toBe(true);
    expect(el.querySelector('.user-code')).toBeNull();
  });

  it('asks before signing out, and Cancel keeps the sign-in', async () => {
    const e = engine({ streamer: { state: 'connected', login: 'gremlinstreamer' } });
    const el = start();
    await settle(30);
    button(el, 'Sign out')!.click();
    flushSync();
    await settle(20);
    const dialog = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('Sign out gremlinstreamer?');
    button(dialog, 'Cancel')!.click();
    await settle(20);
    expect(e.calls.some((c) => c.path.endsWith('/sign-out'))).toBe(false);
    button(el, 'Sign out')!.click();
    flushSync();
    await settle(20);
    button(document.querySelector<HTMLElement>('[role="alertdialog"]')!, 'Sign out')!.click();
    await settle(40);
    expect(e.calls.some((c) => c.method === 'POST' && c.path === '/v1/connections/twitch/streamer/sign-out')).toBe(true);
    expect(el.textContent).toContain('Not signed in');
  });

  it('says why a sign-in ended, and offers to sign in again', async () => {
    engine({ streamer: { state: 'needs_sign_in', detail: 'Twitch no longer accepts this sign-in. Sign in again.' } });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Needs signing in again');
    expect(el.textContent).toContain('Twitch no longer accepts this sign-in');
    expect(button(el, 'Sign in again')).toBeDefined();
  });

  it('shows missing permissions and a way to grant them', async () => {
    engine({ streamer: { state: 'connected', login: 's', missing_scopes: ['user:write:chat'], detail: 'Sign in again to allow: user:write:chat.' } });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('user:write:chat');
    expect(button(el, 'Sign in again')).toBeDefined();
  });

  it('marks a test server plainly', async () => {
    engine({ test_server: 'http://127.0.0.1:7441' });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Test mode.');
    expect(el.textContent).toContain('not the real Twitch');
  });

  it('shows Unknown, not the last answer, when the engine stops answering', async () => {
    const e = engine({ streamer: { state: 'connected', login: 'gremlinstreamer' } });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Signed in as');
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('down'); }));
    await settle(40);
    expect(el.textContent).toContain('Unknown');
    expect(el.textContent).not.toContain('Signed in as');
    void e;
  });

  it('lists which channel events Twitch sends, and why not', async () => {
    engine({
      streamer: { state: 'connected', login: 'gremlinstreamer' },
      chat: {
        state: 'connected', since: '2026-11-08T10:00:00.000Z',
        feeds: [
          { type: 'channel.follow', label: 'Follows', ok: true },
          { type: 'channel.cheer', label: 'Cheers (bits)', ok: false, problem: 'Sign in to Twitch again and allow it (bits:read).' },
        ],
      },
      channel: { title: 'Speedrun night', category: 'Celeste', known: true },
    });
    const el = start();
    await settle(30);
    const list = el.querySelector('ul.feed-list')!;
    expect(list.textContent).toContain('✓ Follows');
    expect(list.textContent).toContain('✗ Cheers (bits): Sign in to Twitch again and allow it (bits:read).');
    expect(el.textContent).toContain('Your channel: “Speedrun night”, in Celeste.');
    await noViolations('feeds');
  });

  it('shows whether chat is read, and sends a message as the bot or as you', async () => {
    const e = engine({
      streamer: { state: 'connected', login: 'gremlinstreamer' },
      bot: { state: 'connected', login: 'gremlinbot' },
      chat: { state: 'connected', since: '2026-11-08T10:00:00.000Z', bot_moderator: false },
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Reading chat.');
    expect(el.textContent).toContain('/mod gremlinbot');
    await noViolations('chat');
    const input = Array.from(el.querySelectorAll<HTMLLabelElement>('label.field')).find((l) => l.textContent?.includes('Send a message'))!.querySelector('input')!;
    input.value = 'hello chat';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Send to chat')!.click();
    await settle(40);
    expect(el.textContent).toContain('Sent as gremlinbot.');
    const asYou = el.querySelector<HTMLInputElement>('input[name="send-as"][value="streamer"]')!;
    asYou.click();
    flushSync();
    input.value = 'from me';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Send to chat')!.click();
    await settle(40);
    const sends = e.calls.filter((c) => c.path === '/v1/chat/messages').map((c) => c.body as { text: string; as: string });
    expect(sends).toEqual([{ text: 'hello chat', as: '', to: 'twitch' }, { text: 'from me', as: 'streamer', to: 'twitch' }]);
    expect(el.textContent).toContain('Sent as gremlinstreamer.');
  });

  it('says plainly when chat is lost, and that missed messages are gone', async () => {
    engine({
      streamer: { state: 'connected', login: 's' },
      chat: { state: 'disconnected', detail: "Lost the chat connection. Trying again. Twitch doesn't resend messages missed while disconnected.", since: '2026-11-08T10:00:00.000Z' },
    });
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('Not reading chat right now.');
    expect(el.textContent).toContain("doesn't resend");
    expect(el.textContent).not.toContain('Reading chat.');
  });

  it('explains a message that was not sent, without retrying', async () => {
    const e = engine({ streamer: { state: 'connected', login: 's' }, chat: { state: 'connected', since: '2026-11-08T10:00:00.000Z' } });
    e.calls.length = 0;
    vi.stubGlobal('fetch', vi.fn(async (path: string, init?: RequestInit) => {
      e.calls.push({ method: init?.method ?? 'GET', path, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (path === '/v1/chat/messages') {
        return new Response(JSON.stringify({ error: { code: 'not_sent', message: "Twitch didn't post the message: held by AutoMod" } }), { status: 422 });
      }
      return new Response(JSON.stringify({ twitch: twitchStatus({ streamer: { state: 'connected', login: 's' }, chat: { state: 'connected', since: 'x' } }).body.twitch }), { status: 200 });
    }));
    const el = start();
    await settle(30);
    const input = Array.from(el.querySelectorAll<HTMLLabelElement>('label.field')).find((l) => l.textContent?.includes('Send a message'))!.querySelector('input')!;
    input.value = 'spicy';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button(el, 'Send to chat')!.click();
    await settle(40);
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Not sent: Twitch didn');
    expect(e.calls.filter((c) => c.path === '/v1/chat/messages')).toHaveLength(1);
  });
});
