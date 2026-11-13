import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import DiscordCard from './DiscordCard.svelte';
import { mockFetch, settle } from '../test/helpers';
import type { DiscordWebhook } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text)!;
const field = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll('label')).find((l) => l.textContent?.trim().startsWith(text))!.querySelector('input') as HTMLInputElement;
const typed = (i: HTMLInputElement, v: string) => {
  i.value = v;
  i.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
};

const LINK = 'https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz';

function engine() {
  const hooks: DiscordWebhook[] = [];
  const calls = mockFetch({
    '/v1/discord/webhooks': () => ({ status: 200, body: { webhooks: hooks } }),
    '/v1/discord/webhooks/announcements': (m, body) => {
      if (m === 'DELETE') {
        hooks.splice(0, 1);
        return { status: 204, body: null };
      }
      const link = (body as { link: string }).link;
      if (!link.includes('/api/webhooks/')) return { status: 400, body: { error: { code: 'invalid_request', message: 'paste the webhook link Discord copies' } } };
      const w = { name: 'announcements', id: '123456789012345678', discord_name: 'Captain Hook', channel_id: '9', added_at: new Date().toISOString() };
      hooks.push(w);
      return { status: 200, body: w };
    },
    '/v1/discord/webhooks/announcements/test': () => ({ status: 200, body: { message_id: '1' } }),
  });
  return calls;
}

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(DiscordCard, { target });
  return target;
}

describe('Discord on Connections', () => {
  it('adds a webhook, sends a test, and forgets it after asking', async () => {
    const calls = engine();
    const el = start();
    await settle(30);
    expect(el.textContent).toContain('No webhooks yet.');
    button(el, 'Add a webhook').click();
    flushSync();
    const add = button(el, 'Add');
    expect(add.disabled).toBe(true);
    typed(field(el, 'Name here'), 'Announcements');
    typed(field(el, 'Webhook link'), LINK);
    expect(add.disabled).toBe(true); // capitals aren't a name
    typed(field(el, 'Name here'), 'announcements');
    expect(field(el, 'Webhook link').type).toBe('password');
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => v.id)).toEqual([]);

    // A bad link says why, and the link isn't kept in the form.
    typed(field(el, 'Webhook link'), 'https://discord.com/channels/1/2');
    button(el, 'Add').click();
    await settle(30);
    expect(el.querySelector('[role=alert]')?.textContent).toContain('paste the webhook link');
    expect(field(el, 'Webhook link').value).toBe('');

    typed(field(el, 'Webhook link'), LINK);
    button(el, 'Add').click();
    await settle(30);
    expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ link: LINK });
    expect(el.textContent).toContain('Added “announcements”: Discord’s webhook “Captain Hook”.');
    expect(el.querySelector('.hook-list')?.textContent).toContain('announcements');
    expect(el.textContent).not.toContain(LINK);

    button(el, 'Send a test').click();
    await settle(30);
    expect(calls.some((c) => c.method === 'POST' && c.path === '/v1/discord/webhooks/announcements/test')).toBe(true);
    expect(el.textContent).toContain('Posted a test through “announcements”.');

    button(el, 'Forget…').click();
    flushSync();
    expect(document.body.textContent).toContain('Forget “announcements”?');
    button(document.body, 'Forget').click();
    await settle(30);
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true);
    expect(el.textContent).toContain('No webhooks yet.');
  });

  it('shows the list as unknown when the engine can’t say', async () => {
    mockFetch({ '/v1/discord/webhooks': () => ({ status: 503, body: { error: { code: 'unavailable', message: 'engine busy' } } }) });
    const el = start();
    await settle(30);
    expect(el.querySelector('[role=alert]')?.textContent).toContain('Unknown: engine busy');
  });
});
