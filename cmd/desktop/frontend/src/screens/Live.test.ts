import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Live from './Live.svelte';
import { forgetCards } from './ViewerCard.svelte';
import { mockFetch, settle, twitchStatus } from '../test/helpers';
import type { ChatterCard, EventRecord, LiveButton, WorkflowSummary } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
  forgetCards();
});

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Live, { target, props: { idleMs: 20, retryMs: 20, refreshMs: 10_000, hoverMs: 10 } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

function setValue(el: HTMLInputElement | HTMLSelectElement, v: string) {
  el.value = v;
  el.dispatchEvent(new Event(el instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }));
}

const now = new Date().toISOString();

const chat = (seq: number, name: string, text: string, extra: Record<string, unknown> = {}, synthetic = false): EventRecord => ({
  seq,
  event_id: `e${seq}`,
  type: 'chat.message.received',
  schema_version: 1,
  source: 'twitch',
  occurred_at: now,
  received_at: now,
  actor: { platform: 'twitch', id: `u${seq}`, login: name.toLowerCase(), display_name: name },
  payload: { message_id: `m${seq}`, text, color: '#1E90FF', chatter: { id: `u${seq}`, login: name.toLowerCase(), display_name: name, badges: [] }, ...extra },
  delivery_quality: synthetic ? 'synthetic' : 'confirmed',
});

const modesOff = { emote_mode: false, follower_mode: false, follower_mode_duration: null, slow_mode: false, slow_mode_wait_time: null,
  subscriber_mode: false, unique_chat_mode: false };

const brb: LiveButton = { id: 'brb', label: 'Be right back', color: 'blue', position: 0, created_at: now, updated_at: now, starts: [] };
const hype: LiveButton = { id: 'hype', label: 'Hype!', color: '', position: 1, created_at: now, updated_at: now, starts: [] };
const brbFlow: WorkflowSummary = {
  id: 'live.brb', name: 'Be right back scene', enabled: true, running: true, version: 1, updated_at: now,
  triggers: [{ type: 'buttons.button.pressed', where: { button_id: 'brb' } }],
};

function engine(opts: { buttons?: LiveButton[]; events?: EventRecord[]; later?: EventRecord[]; signedIn?: boolean; bot?: boolean; place?: 'right' | 'top';
  refuseEvents?: boolean; extra?: Parameters<typeof mockFetch>[0] } = {}) {
  const buttons = [...(opts.buttons ?? [brb, hype])];
  let later = opts.later ?? [];
  let ui = { theme: 'system', text_size: 'standard', density: 'comfortable', more: [], live_buttons: opts.place ?? 'right' };
  const signedIn = opts.signedIn ?? true;
  const routes: Parameters<typeof mockFetch>[0] = {
    '/v1/settings/ui': (method, body) => {
      if (method === 'PUT') ui = { ...ui, ...(body as object) };
      return { status: 200, body: ui };
    },
    '/v1/connections': () =>
      twitchStatus({
        streamer: signedIn ? { state: 'connected', login: 'gremlin', user_id: 'u1' } : {},
        bot: opts.bot ? { state: 'connected', login: 'gremlinbot' } : {},
        chat: signedIn ? { state: 'connected', since: now } : { state: 'off', since: now },
      }),
    '/v1/events': (_m, _b, query) => {
      if (opts.refuseEvents) return { status: 500, body: { error: { code: 'internal', message: 'the database is busy' } } };
      expect(query).toContain('types=chat.message.received');
      if (!query.includes('after=')) return { status: 200, body: { events: opts.events ?? [], head: 10 } };
      const out = later;
      later = [];
      return { status: 200, body: { events: out, head: 20 } };
    },
    '/v1/buttons': () => ({ status: 200, body: { buttons } }),
    '/v1/workflows': () => ({ status: 200, body: { workflows: [brbFlow] } }),
    '/v1/chat/settings': () => ({ status: 200, body: { settings: modesOff } }),
    '/v1/moderation': (method, body) =>
      method === 'POST'
        ? { status: 200, body: { record: { id: 'mod1', from: 'live', platform: 'twitch', created_at: now, ...(body as object), state: 'done',
            settings: (body as { action: string }).action === 'chat_modes' ? { ...modesOff, ...(body as { chat_modes: object }).chat_modes } : undefined },
            duplicate: false } }
        : { status: 200, body: { moderation: [{ id: 'mod0', request_id: 'r', action: 'timeout', platform: 'twitch', login: 'fan', seconds: 600, from: 'live', state: 'done', created_at: now }] } },
    '/v1/chat/messages': (_m, body) => ({ status: 200, body: { message_id: 'x', sent_as: (body as { as: string }).as, login: 'gremlin' } }),
    '/v1/button-order': (_m, body) => {
      const order = (body as { order: string[] }).order;
      buttons.sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id));
      return { status: 200, body: { buttons } };
    },
  };
  for (const id of ['brb', 'hype', 'raid-out']) {
    routes[`/v1/buttons/${id}/press`] = () => {
      const b = buttons.find((x) => x.id === id)!;
      return { status: 200, body: { button: b, event: chat(99, 'x', ''), duplicate: false, started: id === 'brb' ? [brbFlow] : [] } };
    };
    routes[`/v1/buttons/${id}`] = (method, body) => {
      if (method === 'DELETE') {
        buttons.splice(buttons.findIndex((x) => x.id === id), 1);
        return { status: 204, body: null };
      }
      const b = { ...(body as object), id, position: buttons.length, created_at: now, updated_at: now, starts: [] } as unknown as LiveButton;
      const i = buttons.findIndex((x) => x.id === id);
      if (i >= 0) buttons[i] = b;
      else buttons.push(b);
      return { status: i >= 0 ? 200 : 201, body: b };
    };
  }
  return { calls: mockFetch({ ...routes, ...(opts.extra ?? {}) }), buttons };
}

async function noViolations(label: string) {
  const result = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), label).toEqual([]);
}

describe('Live', () => {
  it('shows chat and the buttons, accessibly, and follows new messages', async () => {
    engine({
      events: [chat(3, 'Fan', 'hello there', { chatter: { id: 'u3', login: 'fan', display_name: 'Fan', badges: [{ set_id: 'moderator' }] } })],
      later: [chat(12, 'Other', 'second!'), chat(13, 'Tester', 'a test', {}, true)],
    });
    const el = start();
    await settle(120);
    const log = el.querySelector('[role="log"]')!;
    expect(log.textContent).toContain('Fan');
    // A badge is one letter, with the word for screen readers and on hover.
    const badge = log.querySelector<HTMLElement>('.live-badge[title="Moderator"]')!;
    expect(badge.querySelector('[aria-hidden="true"]')!.textContent).toBe('M');
    expect(badge.querySelector('.visually-hidden')!.textContent).toBe('Moderator');
    expect(log.textContent).toContain(': hello there');
    expect(log.textContent).toContain('second!');
    expect(log.textContent).toContain('Test'); // a test event is marked
    expect(el.textContent).toContain('Reading chat');
    expect(button(el, 'Be right back')).toBeTruthy();
    expect(button(el, 'Be right back')!.title).toBe('Starts “Be right back scene”.');
    await noViolations('chat and buttons');
    button(el, 'Edit buttons')!.click();
    flushSync();
    await noViolations('editing');
    button(el, 'New button')!.click();
    flushSync();
    await noViolations('a new button');
  });

  it('presses a button, saying what it started', async () => {
    const { calls } = engine();
    const el = start();
    await settle();
    button(el, 'Be right back')!.click();
    await settle();
    const press = calls.find((c) => c.path === '/v1/buttons/brb/press')!;
    expect(press.method).toBe('POST');
    expect((press.body as { request_id: string }).request_id).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(el.textContent).toContain('Pressed “Be right back”: started “Be right back scene”.');
    button(el, 'Hype!')!.click();
    await settle();
    expect(el.textContent).toContain('Pressed “Hype!”. No workflow that’s on starts from it.');
    // Each click is its own press.
    const ids = calls.filter((c) => c.path.endsWith('/press')).map((c) => (c.body as { request_id: string }).request_id);
    expect(new Set(ids).size).toBe(2);
  });

  it('sends to chat as you, or as the bot', async () => {
    const { calls } = engine({ bot: true });
    const el = start();
    await settle();
    const input = el.querySelector<HTMLInputElement>('input[placeholder="Send a message"]')!;
    setValue(input, 'hi chat');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/chat/messages')?.body).toEqual({ text: 'hi chat', as: 'streamer' });
    expect(input.value).toBe('');
    setValue(el.querySelector<HTMLSelectElement>('.live-send select')!, 'bot');
    setValue(input, 'from the bot');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(calls.filter((c) => c.path === '/v1/chat/messages')[1].body).toEqual({ text: 'from the bot', as: 'bot' });
  });

  it('says to sign in when nobody is, and never looks live without the engine', async () => {
    engine({ signedIn: false, refuseEvents: true });
    const el = start();
    await settle(80);
    expect(el.querySelector('input[placeholder="Send a message"]')).toBeNull();
    expect(el.textContent).toContain('Sign in to Twitch on Connections');
    expect(el.textContent).toContain('Unknown: not hearing the engine (the database is busy)');
  });

  it('makes, changes, moves and deletes buttons', async () => {
    const { calls, buttons } = engine();
    const el = start();
    await settle();
    button(el, 'Edit buttons')!.click();
    flushSync();
    expect(el.textContent).toContain('Starts “Be right back scene”.');
    expect(el.textContent).toContain('No workflow starts from it yet.');
    button(el, 'New button')!.click();
    flushSync();
    const form = el.querySelector<HTMLFormElement>('form[aria-label="A new button"]')!;
    button(form, 'Save')!.click();
    flushSync();
    expect(form.textContent).toContain('A button needs a label.');
    setValue(form.querySelector<HTMLInputElement>('input')!, 'Raid out!');
    setValue(form.querySelector<HTMLSelectElement>('select')!, 'purple');
    flushSync();
    button(form, 'Save')!.click();
    await settle();
    const put = calls.find((c) => c.method === 'PUT' && c.path === '/v1/buttons/raid-out')!;
    expect(put.body).toEqual({ label: 'Raid out!', color: 'purple' });
    expect(el.textContent).toContain('Saved “Raid out!”. No workflow starts from it yet');

    el.querySelector<HTMLButtonElement>('button[aria-label="Move “Raid out!” earlier"]')!.click();
    await settle();
    expect(calls.find((c) => c.path === '/v1/button-order')?.body).toEqual({ order: ['brb', 'raid-out', 'hype'] });
    expect(buttons.map((b) => b.id)).toEqual(['brb', 'raid-out', 'hype']);

    // Deleting asks first, naming the workflows that stay.
    const del = Array.from(el.querySelectorAll<HTMLElement>('.live-edit-item')).find((li) => li.textContent?.includes('Be right back'))!;
    button(del, 'Delete')!.click();
    flushSync();
    const dialog = document.querySelector('[role="alertdialog"]')!;
    expect(dialog.textContent).toContain('“Be right back” is deleted for good. “Be right back scene” is kept, but won’t start from it any more.');
    button(dialog, 'Delete')!.click();
    await settle();
    expect(calls.some((c) => c.method === 'DELETE' && c.path === '/v1/buttons/brb')).toBe(true);
    expect(el.textContent).toContain('Deleted “Be right back”.');
  });

  it('opens the editor on a workflow for a button, with the button picked', async () => {
    engine();
    const palette = {
      triggers: [{ type: 'buttons.button.pressed', label: 'You press a button', category: 'Buttons', help: 'You press one of your buttons.',
        match: [{ path: 'button_id', label: 'Button', kind: 'button' }],
        values: [{ token: 'button', label: 'The button’s label', expr: 'event.payload.label' }] }],
      actions: [{ name: 'chat.send', label: 'Say in chat', description: 'Sends a message.', group: 'Twitch', external: true,
        params: [{ name: 'message', label: 'Message', type: 'text', editor: 'message', required: true }] }],
    };
    const fetchMock = globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> };
    const inner = fetchMock.getMockImplementation();
    vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) =>
      p === '/v1/palette' ? new Response(JSON.stringify(palette), { status: 200 }) : inner(p, i)));
    const el = start();
    await settle();
    button(el, 'Edit buttons')!.click();
    flushSync();
    button(el, 'Make a workflow')!.click(); // only Hype! has none
    await settle();
    expect(el.textContent).toContain('A workflow for a button');
    const pick = Array.from(el.querySelectorAll('select')).find((s) => s.textContent?.includes('Any button'))!;
    expect(pick.value).toBe('hype');
    expect(pick.textContent).toContain('Be right back');
    await noViolations('make a workflow');
  });

  it('moves the buttons to the top and back, keeping the choice', async () => {
    const { calls } = engine({ place: 'top' });
    const el = start();
    await settle();
    expect(el.querySelector('.live')!.getAttribute('data-place')).toBe('top');
    button(el, 'Right')!.click();
    await settle();
    expect(calls.find((c) => c.method === 'PUT' && c.path === '/v1/settings/ui')?.body).toEqual({ live_buttons: 'right' });
    expect(el.querySelector('.live')!.getAttribute('data-place')).toBe('right');
    expect(button(el, 'Right')!.getAttribute('aria-pressed')).toBe('true');
  });
});

describe('Live viewer card', () => {
  const fanCard: ChatterCard = {
    platform: 'twitch',
    user_id: 'u3',
    twitch: {
      user_id: 'u3', login: 'fan', display_name: 'Fan', created_at: '2019-05-06T12:00:00Z', description: 'I like games',
      follow: { follows: true, followed_at: '2024-03-01T12:00:00Z' }, subscription: { subscribed: true, tier: 2 },
      moderator: true, ban: { banned: false },
      unknown: [{ fact: 'vip', why: 'Sign in to Twitch again and allow StreamGremlin to see this.' }],
      read_at: now,
    },
    viewer: { id: 'v1', platform: 'twitch', user_id: 'u3', login: 'fan', display_name: 'Fan', first_seen: '2026-01-01T12:00:00Z', last_seen: now, messages: 42, balance: 300 },
    currency: 'gremlins',
    messages: [chat(1, 'Fan', 'first one'), chat(3, 'Fan', 'hello there')],
    moderation: [],
    suspicious: null,
  };

  it('opens on hover with Twitch’s facts, unknowns marked, and the record; closes when the pointer leaves', async () => {
    const { calls } = engine({ events: [chat(3, 'Fan', 'hello there')], extra: { '/v1/chatters/twitch/u3': () => ({ status: 200, body: fanCard }) } });
    const el = start();
    await settle(60);
    const name = button(el, 'Fan')!;
    expect(name.getAttribute('aria-haspopup')).toBe('dialog');
    name.dispatchEvent(new PointerEvent('pointerenter'));
    await settle(60);
    const card = document.querySelector('[role="dialog"]')!;
    expect(card).toBeTruthy();
    expect(name.getAttribute('aria-expanded')).toBe('true');
    expect(card.textContent).toContain('Tier 2');
    expect(card.textContent).toContain('Moderator');
    expect(card.textContent).toContain('Unknown: Sign in to Twitch again'); // VIP: unknown, not "No"
    expect(card.textContent).toContain('I like games');
    expect(card.textContent).toContain('42 messages in all');
    expect(card.textContent).toContain('300 gremlins');
    expect(card.textContent).toContain('first one');
    await noViolations('viewer card');
    // Hovering again within a minute doesn't ask Twitch again.
    name.dispatchEvent(new PointerEvent('pointerleave'));
    await settle(300);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    name.dispatchEvent(new PointerEvent('pointerenter'));
    await settle(60);
    expect(document.querySelector('[role="dialog"]')).toBeTruthy();
    expect(calls.filter((c) => c.path.startsWith('/v1/chatters/')).length).toBe(1);
    // Refresh asks again.
    button(document.body, 'Refresh')!.click();
    await settle(30);
    expect(calls.filter((c) => c.path.startsWith('/v1/chatters/')).length).toBe(2);
  });

  it('opens from the keyboard, takes focus, and gives it back on Escape', async () => {
    engine({ events: [chat(3, 'Fan', 'hello there')], extra: { '/v1/chatters/twitch/u3': () => ({ status: 200, body: fanCard }) } });
    const el = start();
    await settle(60);
    const name = button(el, 'Fan')!;
    name.focus();
    name.click();
    await settle(30);
    const card = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(card.contains(document.activeElement)).toBe(true);
    // Pinned: leaving with the pointer doesn't close it.
    name.dispatchEvent(new PointerEvent('pointerleave'));
    await settle(300);
    expect(document.querySelector('[role="dialog"]')).toBeTruthy();
    card.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(name);
  });

  it('says unknown when the engine can’t answer, and doesn’t ask Twitch about a test chatter', async () => {
    const { calls } = engine({
      events: [chat(3, 'Fan', 'hello there'), chat(4, 'Tester', 'a test', {}, true)],
      extra: {
        '/v1/chatters/twitch/u3': () => ({ status: 503, body: { error: { code: 'unavailable', message: 'Twitch isn’t set up' } } }),
        '/v1/chatters/twitch/u4': () => ({ status: 200, body: { platform: 'twitch', user_id: 'u4', twitch: null, viewer: null, currency: 'points', messages: [chat(4, 'Tester', 'a test', {}, true)], moderation: [], suspicious: null } }),
      },
    });
    const el = start();
    await settle(60);
    button(el, 'Fan')!.click();
    await settle(30);
    expect(document.querySelector('[role="dialog"]')!.textContent).toContain('Unknown: Twitch isn’t set up');
    button(el, 'Tester')!.click();
    await settle(30);
    const card = document.querySelector('[role="dialog"]')!;
    expect(card.textContent).toContain('A test chatter');
    expect(card.textContent).not.toContain('On Twitch');
    expect(calls.some((c) => c.path === '/v1/chatters/twitch/u4?test=1')).toBe(true);
  });
});

const suspicious = (seq: number, name: string, messageId: string, text: string): EventRecord => ({
  seq,
  event_id: `s${seq}`,
  type: 'moderation.suspicious_user.message',
  schema_version: 1,
  source: 'twitch',
  occurred_at: now,
  received_at: now,
  actor: { platform: 'twitch', id: `u${seq}`, login: name.toLowerCase(), display_name: name },
  payload: { message_id: messageId, text, low_trust_status: 'active_monitoring', types: ['ban_evader'], ban_evasion: 'likely', shared_ban_channel_ids: [] },
  delivery_quality: 'confirmed',
});

describe('Live moderating', () => {
  const posts = (calls: { method: string; path: string; body: unknown }[]) =>
    calls.filter((c) => c.method === 'POST' && c.path === '/v1/moderation').map((c) => c.body as Record<string, unknown>);

  it('times out, deletes and bans from a chat line, banning only after asking', async () => {
    const { calls } = engine({ events: [chat(3, 'Fan', 'buy followers'), chat(4, 'Gremlin', 'my own', { chatter: { id: 'u1', login: 'gremlin', display_name: 'Gremlin', badges: [] } }),
      chat(5, 'Tester', 'a test', {}, true)] });
    const el = start();
    await settle(60);
    const lineOf = (name: string) => Array.from(el.querySelectorAll('.live-line')).find((li) => li.querySelector('.live-name')?.textContent === name)!;
    // No gear on your own line or a test line.
    expect(lineOf('Gremlin').querySelector('.live-gear')).toBeNull();
    expect(lineOf('Tester').querySelector('.live-gear')).toBeNull();
    // The gear sits right after the name, so long messages don't push it away.
    const gear = lineOf('Fan').querySelector<HTMLButtonElement>('.live-gear')!;
    expect(gear.previousElementSibling?.classList.contains('live-name')).toBe(true);
    expect(gear.getAttribute('aria-label')).toBe('Moderate Fan');
    expect(gear.getAttribute('aria-expanded')).toBe('false');
    expect(document.querySelector('[role="menu"]')).toBeNull();
    const menu = () => document.querySelector<HTMLElement>('[role="menu"]');
    const item = (text: string) =>
      Array.from(menu()!.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')).find((b) => b.textContent?.trim() === text)!;
    gear.click();
    flushSync();
    expect(gear.getAttribute('aria-expanded')).toBe('true');
    expect(Array.from(menu()!.querySelectorAll('[role="menuitem"]')).map((b) => b.textContent?.trim())).toEqual([
      'Delete message…',
      'Time out 10 minutes…',
      'Ban…',
      'More in their card…',
    ]);
    await noViolations('gear menu');
    item('Time out 10 minutes…').click();
    flushSync();
    expect(menu()).toBeNull(); // choosing closes it
    // It asks first; Cancel sends nothing.
    const ask = () => document.querySelector<HTMLElement>('[role="alertdialog"]');
    expect(ask()!.textContent).toContain('Time out Fan for 10 minutes?');
    button(ask()!, 'Cancel')!.click();
    flushSync();
    expect(ask()).toBeNull();
    expect(posts(calls)).toHaveLength(0);
    gear.click();
    flushSync();
    item('Time out 10 minutes…').click();
    flushSync();
    button(ask()!, 'Time out')!.click();
    await settle(20);
    expect(posts(calls)[0]).toMatchObject({ action: 'timeout', seconds: 600, user_id: 'u3', login: 'fan', platform: 'twitch', from: 'live' });
    expect(el.textContent).toContain('Timed out Fan for 10 minutes.');
    gear.click();
    flushSync();
    item('Delete message…').click();
    flushSync();
    expect(posts(calls)).toHaveLength(1);
    expect(ask()!.textContent).toContain('Delete Fan’s message?');
    expect(ask()!.textContent).toContain('“buy followers”');
    button(ask()!, 'Delete')!.click();
    await settle(20);
    expect(posts(calls)[1]).toMatchObject({ action: 'delete', message_id: 'm3' });
    expect(lineOf('Fan').textContent).toContain('Deleted');
    // A deleted line keeps its gear, without Delete.
    lineOf('Fan').querySelector<HTMLButtonElement>('.live-gear')!.click();
    flushSync();
    expect(Array.from(menu()!.querySelectorAll('[role="menuitem"]')).map((b) => b.textContent?.trim())).not.toContain('Delete message…');
    lineOf('Fan').querySelector<HTMLButtonElement>('.live-gear')!.click(); // the gear again closes it
    flushSync();
    expect(menu()).toBeNull();
    // Each request has its own id.
    expect(posts(calls)[0].request_id).not.toBe(posts(calls)[1].request_id);
  });

  it('asks before a ban, and says when Twitch refuses', async () => {
    const { calls } = engine({
      events: [chat(3, 'Fan', 'spam')],
      extra: {
        '/v1/moderation': () => ({ status: 200, body: { record: { id: 'm', request_id: 'r', action: 'ban', platform: 'twitch', from: 'live', created_at: now,
          state: 'refused', answer: 'Twitch refused: The user specified in the user_id field is already banned. (400)' }, duplicate: false } }),
      },
    });
    const el = start();
    await settle(60);
    el.querySelector<HTMLButtonElement>('.live-gear')!.click();
    flushSync();
    button(document.querySelector('[role="menu"]')!, 'Ban…')!.click();
    flushSync();
    expect(document.querySelector('[role="menu"]')).toBeNull();
    const dialog = document.querySelector('[role="alertdialog"], [role="dialog"]')!;
    expect(dialog.textContent).toContain('Ban Fan?');
    expect(posts(calls)).toHaveLength(0);
    await noViolations('ban dialog');
    button(dialog, 'Ban')!.click();
    await settle(20);
    expect(posts(calls)[0]).toMatchObject({ action: 'ban', user_id: 'u3' });
    expect(el.querySelector('[role="alert"]')!.textContent).toContain('Not done: Twitch refused');
  });

  it('works the gear menu from the keyboard, and opens the card from it', async () => {
    engine({ events: [chat(3, 'Fan', 'hello'), chat(4, 'Pal', 'hi')] });
    const el = start();
    await settle(60);
    const gear = el.querySelector<HTMLButtonElement>('.live-gear')!;
    const key = (target: Element, k: string) => target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
    gear.focus();
    gear.click();
    await settle(5);
    const items = () => Array.from(document.querySelectorAll<HTMLButtonElement>('[role="menu"] [role="menuitem"]'));
    expect(document.activeElement).toBe(items()[0]); // focus moves into the menu
    key(document.activeElement!, 'ArrowDown');
    expect(document.activeElement).toBe(items()[1]);
    key(document.activeElement!, 'ArrowUp');
    key(document.activeElement!, 'ArrowUp'); // wraps
    expect(document.activeElement).toBe(items()[items().length - 1]);
    key(document.activeElement!, 'Home');
    expect(document.activeElement).toBe(items()[0]);
    key(document.activeElement!, 'Escape');
    flushSync();
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(gear); // focus comes back
    // A click elsewhere closes it.
    gear.click();
    flushSync();
    document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    flushSync();
    expect(document.querySelector('[role="menu"]')).toBeNull();
    // Only one menu at a time.
    gear.click();
    flushSync();
    el.querySelectorAll<HTMLButtonElement>('.live-gear')[1].click();
    flushSync();
    expect(document.querySelectorAll('[role="menu"]')).toHaveLength(1);
    expect(el.querySelectorAll('.live-gear')[1].getAttribute('aria-expanded')).toBe('true');
    expect(gear.getAttribute('aria-expanded')).toBe('false');
    // The last item opens the card, pinned, with every action.
    gear.click();
    flushSync();
    button(document.querySelector('[role="menu"]')!, 'More in their card…')!.click();
    await settle(30);
    expect(document.querySelector('[role="menu"]')).toBeNull();
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain('Fan');
  });

  it('marks a suspicious user’s message, in either order, and shows one chat didn’t carry', async () => {
    engine({
      events: [chat(3, 'Fan', 'hello'), suspicious(4, 'Fan', 'm3', 'hello')],
      later: [suspicious(12, 'Sneaky', 'm99', 'only mods see this'), chat(13, 'Sneaky', 'only mods see this', { message_id: 'm99' })],
    });
    const el = start();
    await settle(150);
    const lines = Array.from(el.querySelectorAll('.live-line'));
    expect(lines).toHaveLength(2); // two messages, each one line
    expect(lines[0].textContent).toContain('Suspicious: Likely ban evader, monitored');
    expect(lines[1].textContent).toContain('only mods see this');
    expect(lines[1].textContent).toContain('Suspicious');
    await noViolations('suspicious');
  });

  it('turns chat modes on and off, and says unknown when Twitch can’t be asked', async () => {
    const { calls } = engine();
    const el = start();
    await settle(60);
    const slow = button(el, 'Slow')!;
    expect(slow.getAttribute('aria-pressed')).toBe('false');
    slow.click();
    await settle(20);
    expect(posts(calls)[0]).toMatchObject({ action: 'chat_modes', chat_modes: { slow_mode: true, slow_mode_wait_time: 30 } });
    expect(button(el, 'Slow')!.getAttribute('aria-pressed')).toBe('true');
    expect(el.textContent).toContain('On: slow (30 seconds).');
    await noViolations('chat modes');
    // The log of what was done.
    const details = el.querySelector<HTMLDetailsElement>('.live-log-details')!;
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    await settle(20);
    expect(details.textContent).toContain('fan: Time out 10 minutes');
  });

  it('says the chat modes are unknown when Twitch isn’t signed in', async () => {
    engine({ signedIn: false, extra: { '/v1/chat/settings': () => ({ status: 200, body: { settings: null, problem: 'Sign in with your Twitch account first.' } }) } });
    const el = start();
    await settle(60);
    expect(el.querySelector('.live-modes')!.textContent).toContain('Unknown: Sign in with your Twitch account first.');
    expect(button(el, 'Slow')).toBeUndefined();
  });

  it('moderates from the card: a timeout with a reason, a warning needs one, a ban asks first', async () => {
    let cardCalls = 0;
    const card: ChatterCard = {
      platform: 'twitch', user_id: 'u3',
      twitch: { user_id: 'u3', login: 'fan', vip: false, moderator: false, ban: { banned: false }, follow: { follows: false }, subscription: { subscribed: false },
        unknown: [], read_at: now },
      viewer: null, currency: 'points', messages: [], suspicious: { treatment: 'active_monitoring', ban_evasion: 'possible', types: ['ban_evader'], shared_bans: 0, at: now, seq: 4 },
      moderation: [{ id: 'old', request_id: 'r0', action: 'warn', platform: 'twitch', reason: 'be nice', from: 'live', state: 'done', created_at: now }],
    };
    const { calls } = engine({ events: [chat(3, 'Fan', 'hello')], extra: { '/v1/chatters/twitch/u3': () => ((cardCalls++), { status: 200, body: card }) } });
    const el = start();
    await settle(60);
    button(el, 'Fan')!.click();
    await settle(30);
    const dlg = document.querySelector<HTMLElement>('.viewer-card')!;
    expect(dlg.textContent).toContain('Possible ban evader, monitored');
    expect(dlg.textContent).toContain('Warn (“be nice”)');
    await noViolations('card with moderating');
    button(dlg, 'Warn…')!.click();
    await settle(10);
    expect(dlg.textContent).toContain('A warning needs a reason');
    expect(document.querySelector('[role="alertdialog"]')).toBeNull(); // nothing to ask about yet
    expect(posts(calls)).toHaveLength(0);
    const reason = dlg.querySelector<HTMLInputElement>('input')!;
    setValue(reason, 'calm down');
    setValue(dlg.querySelector<HTMLSelectElement>('select')!, '3600');
    button(dlg, 'Time out…')!.click();
    flushSync();
    expect(posts(calls)).toHaveLength(0);
    const ask = () => document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    expect(ask().textContent).toContain('Time out Fan for 1 hour?');
    expect(ask().textContent).toContain('Reason: “calm down”');
    button(ask(), 'Time out')!.click();
    await settle(30);
    expect(posts(calls)[0]).toMatchObject({ action: 'timeout', seconds: 3600, reason: 'calm down', user_id: 'u3' });
    expect(dlg.textContent).toContain('Timed out Fan for 1 hour.');
    expect(cardCalls).toBe(2); // read again after acting
    button(dlg, 'Ban…')!.click();
    flushSync();
    expect(posts(calls)).toHaveLength(1);
    const confirm = Array.from(document.querySelectorAll('[role="alertdialog"], [role="dialog"]')).find((d) => d.textContent?.includes('Ban Fan?'))!;
    button(confirm, 'Ban')!.click();
    await settle(30);
    expect(posts(calls)[1]).toMatchObject({ action: 'ban' });
    // Easy to undo: no question.
    button(document.querySelector('.viewer-card')!, 'Restrict')!.click();
    await settle(30);
    expect(posts(calls)[2]).toMatchObject({ action: 'restrict' });
    button(document.querySelector('.viewer-card')!, 'Make VIP')!.click();
    await settle(30);
    expect(posts(calls)[3]).toMatchObject({ action: 'vip' });
    // Hard to undo: each asks, and Cancel sends nothing.
    for (const [label, title, confirm, action] of [
      ['Warn…', 'Warn Fan?', 'Warn', 'warn'],
      ['Shoutout…', 'Shout out Fan?', 'Shout out', 'shoutout'],
      ['Make moderator…', 'Make Fan a moderator?', 'Make moderator', 'mod'],
    ]) {
      const before = posts(calls).length;
      setValue(document.querySelector<HTMLInputElement>('.viewer-card input')!, 'be kind');
      button(document.querySelector('.viewer-card')!, label)!.click();
      flushSync();
      expect(ask().textContent).toContain(title);
      button(ask(), 'Cancel')!.click();
      flushSync();
      expect(posts(calls)).toHaveLength(before);
      button(document.querySelector('.viewer-card')!, label)!.click();
      flushSync();
      button(ask(), confirm)!.click();
      await settle(30);
      expect(posts(calls)[before]).toMatchObject({ action });
    }
  });
});
