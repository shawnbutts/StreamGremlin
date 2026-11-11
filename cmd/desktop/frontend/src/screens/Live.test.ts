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
        streamer: signedIn ? { state: 'connected', login: 'gremlin' } : {},
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
    expect(log.textContent).toContain('Moderator');
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
        '/v1/chatters/twitch/u4': () => ({ status: 200, body: { platform: 'twitch', user_id: 'u4', twitch: null, viewer: null, currency: 'points', messages: [chat(4, 'Tester', 'a test', {}, true)] } }),
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
