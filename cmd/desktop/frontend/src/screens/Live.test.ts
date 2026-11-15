import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import Live from './Live.svelte';
import { forgetCards } from './ViewerCard.svelte';
import { found, mockFetch, settle, twitchStatus } from '../test/helpers';
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

const brb: LiveButton = { id: 'brb', label: 'Be right back', color: 'blue', icon: '', hide_label: false, position: 0, created_at: now, updated_at: now, starts: [] };
const hype: LiveButton = { id: 'hype', label: 'Hype!', color: '', icon: '', hide_label: false, position: 1, created_at: now, updated_at: now, starts: [] };
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
      expect(query).toContain('chat.message.sent');
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
    '/v1/chat/messages': (_m, body) => ({ status: 200, body: { twitch: { message_id: 'x', sent_as: (body as { as: string }).as, login: 'gremlin' } } }),
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

  it('shows both chats in one list, and sends to both by default', async () => {
    const yt = {
      ...chat(4, 'YouTube Viewer', 'from YouTube', { chatter: { id: 'UCv', display_name: 'YouTube Viewer', is_member: true, channel_url: 'https://www.youtube.com/channel/UCv' } }),
      source: 'youtube',
      actor: { platform: 'youtube', id: 'UCv', display_name: 'YouTube Viewer' },
    };
    const sent: unknown[] = [];
    const { calls } = engine({
      events: [chat(3, 'Fan', 'hello there'), yt],
      extra: {
        '/v1/connections/youtube': () => ({
          status: 200,
          body: { chat: { state: 'connected', since: now, broadcast: { id: 'bc1', title: 'Friday stream' } }, bot: { state: 'signed_out' } },
        }),
        '/v1/chat/messages': (_m, body) => {
          sent.push(body);
          return { status: 200, body: { twitch: { message_id: 't', sent_as: 'streamer', login: 'gremlin' }, youtube: { message_ids: ['y'], sent_as: 'streamer', channel: 'Gremlin' } } };
        },
      },
    });
    const el = start();
    await settle(120);
    const log = el.querySelector('[role="log"]')!;
    const lines = Array.from(log.querySelectorAll('li'));
    expect(lines[0].textContent).toContain('Twitch');
    expect(lines[1].textContent).toContain('YouTube');
    expect(lines[1].textContent).toContain(': from YouTube');
    expect(lines[1].querySelector('.live-badge[title="Member"]')).toBeTruthy();
    // Without your YouTube channel signed in, YouTube lines can't be moderated.
    expect(lines[0].querySelector('.live-gear')).toBeTruthy();
    expect(lines[1].querySelector('.live-gear')).toBeNull();
    expect(el.textContent).toContain('YouTube: reading the chat of “Friday stream”');

    const to = Array.from(el.querySelectorAll<HTMLSelectElement>('.live-send select')).find((s) => s.querySelector('option[value="both"]'))!;
    expect(to.value).toBe('both');
    expect(el.textContent).toContain('Each message to YouTube uses 20');
    const input = el.querySelector<HTMLInputElement>('input[placeholder="Send a message"]')!;
    setValue(input, 'hi both');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(sent).toEqual([{ text: 'hi both', as: 'streamer', to: 'both' }]);
    setValue(to, 'youtube');
    setValue(input, 'just YouTube');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(sent[1]).toEqual({ text: 'just YouTube', as: 'streamer', to: 'youtube' });
    expect(calls.length).toBeGreaterThan(0);
    await noViolations('with YouTube');
  });

  it('says when a message reached only one chat', async () => {
    engine({
      extra: {
        '/v1/connections/youtube': () => ({ status: 200, body: { chat: { state: 'connected', since: now, broadcast: { id: 'b', title: 'T' } } } }),
        '/v1/chat/messages': () => ({
          status: 200,
          body: { youtube: { message_ids: ['y'], sent_as: 'streamer', channel: 'G' }, failed: { twitch: 'too many chat messages; nothing was sent' } },
        }),
      },
    });
    const el = start();
    await settle(120);
    setValue(el.querySelector<HTMLInputElement>('input[placeholder="Send a message"]')!, 'x');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('Not sent to Twitch: too many chat messages');
  });

  it('hides YouTube until it’s set up', async () => {
    engine({ extra: { '/v1/connections/youtube': () => ({ status: 200, body: { chat: { state: 'off', since: now } } }) } });
    const el = start();
    await settle(120);
    expect(el.textContent).not.toContain('YouTube');
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
    expect(calls.find((c) => c.path === '/v1/chat/messages')?.body).toEqual({ text: 'hi chat', as: 'streamer', to: 'twitch' });
    expect(input.value).toBe('');
    setValue(el.querySelector<HTMLSelectElement>('.live-send select')!, 'bot');
    setValue(input, 'from the bot');
    flushSync();
    button(el, 'Send')!.click();
    await settle();
    expect(calls.filter((c) => c.path === '/v1/chat/messages')[1].body).toEqual({ text: 'from the bot', as: 'bot', to: 'twitch' });
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
    expect(put.body).toEqual({ label: 'Raid out!', color: 'purple', icon: '', hide_label: false });
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

  it('gives a button an icon, and can hide its label', async () => {
    const { calls } = engine();
    const el = start();
    await settle();
    button(el, 'Edit buttons')!.click();
    flushSync();
    const item = Array.from(el.querySelectorAll<HTMLElement>('.live-edit-item')).find((li) => li.textContent?.includes('Be right back'))!;
    button(item, 'Edit')!.click();
    await settle(30);
    const form = el.querySelector<HTMLFormElement>('form[aria-label="Change a button"]')!;
    const only = form.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(only.disabled).toBe(true); // no icon yet
    // Suggestions before any search; a search narrows them.
    const choices = () => Array.from(form.querySelectorAll<HTMLButtonElement>('.live-icon-grid button'));
    expect(choices().length).toBeGreaterThan(5);
    setValue(form.querySelector<HTMLInputElement>('input[type="search"]')!, 'player pause');
    await settle(10);
    const pause = choices().find((b) => b.getAttribute('aria-label') === 'player-pause')!;
    expect(pause).toBeTruthy();
    expect(choices().every((b) => b.getAttribute('aria-label')!.includes('pause'))).toBe(true);
    pause.click();
    flushSync();
    expect(pause.getAttribute('aria-pressed')).toBe('true');
    expect(only.disabled).toBe(false);
    only.click();
    flushSync();
    button(form, 'Save')!.click();
    await settle();
    const put = calls.find((c) => c.method === 'PUT' && c.path === '/v1/buttons/brb')!;
    expect(put.body).toEqual({ label: 'Be right back', color: 'blue', icon: 'tabler:player-pause', hide_label: true });
    // On the screen: the icon only, with the label still its name.
    button(el, 'Done')!.click();
    await settle(30);
    const press = el.querySelector<HTMLButtonElement>('.live-press')!;
    expect(press.querySelector('svg')).toBeTruthy();
    expect(press.querySelector('.visually-hidden')!.textContent).toBe('Be right back');
    expect(press.title).toContain('Be right back');
    await noViolations('icon buttons');
  });

  it('picks an icon from Nerd Fonts too', async () => {
    const { calls } = engine();
    const el = start();
    await settle();
    button(el, 'Edit buttons')!.click();
    flushSync();
    button(el, 'New button')!.click();
    const form = await found<HTMLFormElement>(el, 'form[aria-label="A new button"]');
    setValue(form.querySelector<HTMLInputElement>('input')!, 'Mute');
    setValue(form.querySelector<HTMLSelectElement>('select[aria-label="Icon set"]')!, 'nf');
    // Nerd Fonts' names load when first chosen.
    setValue(await found<HTMLInputElement>(form, 'input[type="search"]'), 'microphone off');
    (await found<HTMLButtonElement>(form, '.live-icon-grid button[aria-label="md-microphone_off"]')).click();
    flushSync();
    expect(form.querySelector('.live-icon-now')!.textContent).toContain('md-microphone_off');
    button(form, 'Save')!.click();
    await settle();
    const put = calls.find((c) => c.method === 'PUT' && c.path === '/v1/buttons/mute')!;
    expect(put.body).toMatchObject({ icon: 'nf:md-microphone_off' });
  });

  it('packs icon-only buttons into a grid, keeping their order', async () => {
    engine({ buttons: [
      { ...brb, icon: 'tabler:player-pause', hide_label: true },
      { ...hype, icon: 'nf:md-fire', hide_label: true },
      { ...brb, id: 'end', label: 'End the stream', position: 2 },
      { ...hype, id: 'mute', label: 'Mute', icon: 'nf:md-microphone_off', hide_label: true, position: 3 },
    ] });
    const el = start();
    await settle(80);
    const items = Array.from(el.querySelectorAll<HTMLElement>('.live-grid > li'));
    expect(items.map((li) => li.classList.contains('live-tile'))).toEqual([true, true, false, true]);
    expect(items.map((li) => li.querySelector('.visually-hidden, .live-press-label')?.textContent)).toEqual(['Be right back', 'Hype!', 'End the stream', 'Mute']);
    expect(items[1].querySelector('.nf-glyph')).toBeTruthy();
    await noViolations('icon grid');
  });

  it('shows the label when an icon isn’t known, even if hidden', async () => {
    engine({ buttons: [{ ...brb, icon: 'tabler:no-such-icon-anywhere', hide_label: true }] });
    const el = start();
    await settle(60);
    const press = el.querySelector<HTMLButtonElement>('.live-press')!;
    expect(press.querySelector('svg')).toBeNull();
    expect(press.querySelector('.visually-hidden')).toBeNull();
    expect(press.textContent).toContain('Be right back');
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

  it('moderates YouTube lines as your channel, and strikes through deleted messages (D-100)', async () => {
    const ytChat = (seq: number, id: string, name: string, text: string) => ({
      ...chat(seq, name, text, { message_id: `LCC.${seq}`, chatter: { id, display_name: name } }),
      source: 'youtube',
      actor: { platform: 'youtube', id, display_name: name },
    });
    const { calls } = engine({
      events: [ytChat(3, 'UCfan', 'Fan', 'spam here'), ytChat(4, 'UCme', 'Me', 'my own'), ytChat(5, 'UCother', 'Other', 'deleted elsewhere')],
      later: [
        { ...ytChat(6, 'UCother', 'Other', ''), type: 'chat.message.deleted', actor: undefined, payload: { message_id: 'LCC.5' } },
        { ...ytChat(7, 'UCstreamer', 'Me', ''), type: 'chat.members_only.changed', actor: undefined, payload: { enabled: true } },
      ],
      extra: {
        '/v1/connections/youtube': () => ({
          status: 200,
          body: { streamer: { state: 'connected', channel: { id: 'UCme', title: 'Me' } }, bot: { state: 'signed_out' },
            chat: { state: 'connected', since: now, broadcast: { id: 'bc1', title: 'Friday stream', live_chat_id: 'c1' } } },
        }),
      },
    });
    const el = start();
    await settle(120);
    const lineOf = (name: string) => Array.from(el.querySelectorAll('.live-line')).find((li) => li.querySelector('.live-name')?.textContent === name)!;
    expect(lineOf('Me').querySelector('.live-gear')).toBeNull(); // your own channel
    expect(lineOf('Other').textContent).toContain('Deleted'); // YouTube said it was deleted
    expect(el.textContent).toContain('members-only chat turned on');
    expect(el.textContent).toContain('use YouTube Studio');
    lineOf('Fan').querySelector<HTMLButtonElement>('.live-gear')!.click();
    flushSync();
    button(document.querySelector('[role="menu"]')!, 'Delete message…')!.click();
    flushSync();
    const ask = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    expect(ask.textContent).toContain('removed from YouTube chat');
    expect(ask.textContent).toContain('20 of today’s YouTube quota units');
    button(ask, 'Delete')!.click();
    await settle(20);
    expect(posts(calls)[0]).toMatchObject({ action: 'delete', message_id: 'LCC.3', user_id: 'UCfan', login: 'Fan', platform: 'youtube', from: 'live' });
    expect(lineOf('Fan').textContent).toContain('Deleted');
    lineOf('Fan').querySelector<HTMLButtonElement>('.live-gear')!.click();
    flushSync();
    button(document.querySelector('[role="menu"]')!, 'Ban…')!.click();
    flushSync();
    const ban = document.querySelector<HTMLElement>('[role="alertdialog"]')!;
    expect(ban.textContent).toContain('200 of today’s YouTube quota units');
    expect(ban.textContent).not.toContain('give a reason'); // YouTube takes none
    await noViolations('YouTube moderating');
  });

  it('shows the bot’s and workflows’ own messages, marked, with nothing to moderate (D-087)', async () => {
    engine({
      events: [chat(3, 'Fan', '!uptime')],
      later: [
        { ...chat(4, 'GremlinBot', 'We’ve been live for 2 hours.', { sent_by: 'bot' }), type: 'chat.message.sent' },
        { ...chat(5, 'Gremlin', 'Thanks for the follow!', { sent_by: 'workflow' }), type: 'chat.message.sent' },
      ],
    });
    const el = start();
    await settle(80);
    const lineOf = (name: string) => Array.from(el.querySelectorAll('.live-line')).find((li) => li.querySelector('.live-name')?.textContent === name)!;
    expect(lineOf('GremlinBot').textContent).toContain('Bot');
    expect(lineOf('GremlinBot').textContent).toContain('We’ve been live for 2 hours.');
    expect(lineOf('Gremlin').textContent).toContain('Workflow');
    expect(lineOf('GremlinBot').querySelector('.live-gear')).toBeNull();
    expect(lineOf('Fan').querySelector('.live-gear')).not.toBeNull();
    await noViolations('own messages');
  });

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

  it('locks chat in one press, unlocks it, and turns Shield Mode on (D-105)', async () => {
    let locked: Record<string, unknown> | null = null;
    let shieldOn = false;
    const { calls } = engine({
      extra: {
        '/v1/chat/settings': () => ({ status: 200, body: { settings: modesOff, shield: { active: shieldOn }, lock: locked } }),
        '/v1/moderation': (method, body) => {
          if (method !== 'POST') return { status: 200, body: { moderation: [] } };
          const b = body as { action: string; on?: boolean };
          let answer = '';
          if (b.action === 'lock') {
            locked = { id: 'lk', action: 'lock', platform: 'twitch', from: 'live', state: 'done', created_at: now };
            shieldOn = true;
            answer = 'Locked: Shield Mode, emote-only and followers-only (followed 10 minutes) on.';
          } else if (b.action === 'unlock') {
            locked = null;
            shieldOn = false;
            answer = 'Unlocked: chat is back as it was before the lock.';
          } else if (b.action === 'shield_mode') shieldOn = !!b.on;
          return { status: 200, body: { record: { id: 'm', from: 'live', platform: 'twitch', created_at: now, ...b, state: 'done', answer }, duplicate: false } };
        },
      },
    });
    const el = start();
    await settle(60);
    const modes = el.querySelector('.live-modes')!;
    expect(modes.textContent).toContain('Shield Mode, emote-only and followers-only (followed at least 10 minutes) on');
    expect(button(el, 'Shield Mode')!.getAttribute('aria-pressed')).toBe('false');
    await noViolations('lock offered');
    button(el, 'Lock chat')!.click();
    await settle(30);
    expect(posts(calls).at(-1)).toMatchObject({ action: 'lock', platform: 'twitch', from: 'live' });
    expect(modes.textContent).toContain('Locked: Shield Mode, emote-only and followers-only');
    expect(modes.textContent).toContain('Chat locked since');
    expect(button(el, 'Shield Mode')!.getAttribute('aria-pressed')).toBe('true');
    expect(button(el, 'Lock chat')).toBeUndefined();
    await noViolations('locked');
    button(el, 'Unlock chat')!.click();
    await settle(30);
    expect(posts(calls).at(-1)).toMatchObject({ action: 'unlock' });
    expect(modes.textContent).toContain('Unlocked: chat is back as it was');
    button(el, 'Shield Mode')!.click();
    await settle(30);
    expect(posts(calls).at(-1)).toMatchObject({ action: 'shield_mode', on: true });
    expect(modes.textContent).toContain('Shield Mode is on.');
    // The explanations are on hover (and read out), not on the panel.
    expect(button(el, 'Shield Mode')!.title).toBe('Uses the settings you chose in Twitch’s Mod View.');
    expect(button(el, 'Lock chat')!.title).toContain('Shield Mode, emote-only and followers-only');
    // Mod View opens in the browser, where Shield Mode is set up.
    Array.from(modes.querySelectorAll('button')).find((b) => b.textContent?.startsWith('Mod View'))!.click();
    await settle(20);
    expect(calls.find((c) => c.path === '/desktop/open-url')?.body).toEqual({ url: 'https://www.twitch.tv/moderator/gremlin' });
  });

  it('says Shield Mode is unknown when Twitch can’t say', async () => {
    engine({ extra: { '/v1/chat/settings': () => ({ status: 200, body: { settings: modesOff, shield: null, shield_problem: 'Sign in to Twitch again and allow it.', lock: null } }) } });
    const el = start();
    await settle(60);
    expect(el.querySelector('.live-modes')!.textContent).toContain('Shield Mode: unknown: Sign in to Twitch again and allow it.');
    expect(button(el, 'Shield Mode')).toBeUndefined();
  });

  it('says the chat modes are unknown when Twitch isn’t signed in', async () => {
    engine({ signedIn: false, extra: { '/v1/chat/settings': () => ({ status: 200, body: { settings: null, problem: 'Sign in with your Twitch account first.' } }) } });
    const el = start();
    await settle(60);
    expect(el.querySelector('.live-modes')!.textContent).toContain('Unknown: Sign in with your Twitch account first.');
    expect(button(el, 'Slow')).toBeUndefined();
  });

  it('moderates a YouTube viewer from their card, undoing only a ban made here (D-100)', async () => {
    const yt = {
      ...chat(3, 'Fan', 'hello', { message_id: 'LCC.3', chatter: { id: 'UCfan', display_name: 'Fan', is_moderator: true } }),
      source: 'youtube',
      actor: { platform: 'youtube', id: 'UCfan', display_name: 'Fan' },
    };
    let card: ChatterCard = {
      platform: 'youtube', user_id: 'UCfan', twitch: null, viewer: null, currency: 'points', messages: [], suspicious: null,
      moderation: [{ id: 'm1', request_id: 'r1', action: 'timeout', platform: 'youtube', login: 'Fan', seconds: 600, from: 'filter', state: 'done',
        created_at: now, result_id: 'ban1' }],
    };
    card.youtube_ban = card.moderation[0];
    const { calls } = engine({
      events: [yt],
      extra: {
        '/v1/chatters/youtube/UCfan': () => ({ status: 200, body: card }),
        '/v1/connections/youtube': () => ({
          status: 200,
          body: { streamer: { state: 'connected', channel: { id: 'UCme', title: 'Me' } }, bot: { state: 'signed_out' },
            chat: { state: 'connected', since: now, broadcast: { id: 'bc1', title: 'T', live_chat_id: 'c1' } } },
        }),
      },
    });
    const el = start();
    await settle(120);
    button(el, 'Fan')!.click();
    await settle(30);
    const dlg = document.querySelector<HTMLElement>('.viewer-card')!;
    expect(dlg.textContent).toContain('Moderate on YouTube');
    expect(dlg.textContent).toContain('Time out 10 minutes on YouTube, by a chat filter');
    expect(dlg.textContent).toContain('200 of today’s YouTube quota units');
    expect(dlg.textContent).not.toContain('A test chatter');
    expect(button(dlg, 'Warn…')).toBeUndefined(); // YouTube has no warnings
    expect(button(dlg, 'Remove moderator')).toBeTruthy(); // their message says they're a moderator
    await noViolations('YouTube card');
    card = { ...card, youtube_ban: null };
    button(dlg, 'End the timeout')!.click();
    await settle(30);
    expect(posts(calls)[0]).toMatchObject({ action: 'unban', user_id: 'UCfan', platform: 'youtube' });
    expect(button(dlg, 'Ban…')).toBeTruthy();
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

  it('shows the YouTube viewer count, unknown when YouTube doesn’t say, and changes the title', async () => {
    let stream: Record<string, unknown> = { viewers: 42, title: 'Friday stream', description: 'Chill', checked_at: now };
    const puts: unknown[] = [];
    engine({
      events: [],
      extra: {
        '/v1/connections/youtube': () => ({
          status: 200,
          body: { chat: { state: 'connected', since: now, broadcast: { id: 'bc1', title: 'Friday stream' }, stream }, bot: { state: 'signed_out' } },
        }),
        '/v1/connections/youtube/stream': (_m, body) => {
          puts.push(body);
          stream = { ...stream, ...(body as object) };
          return { status: 200, body: stream };
        },
      },
    });
    const el = start();
    await settle(120);
    expect(el.textContent).toContain('42 watching');
    const panel = el.querySelector('[aria-labelledby="live-yt-stream-heading"]')!;
    expect(panel.textContent).toContain('Title: Friday stream');
    await noViolations('youtube stream');
    button(panel, 'Change title or description…')!.click();
    flushSync();
    const form = panel.querySelector('form')!;
    expect(form.textContent).toContain('51 of today’s YouTube quota units');
    setValue(form.querySelector<HTMLInputElement>('input')!, 'Late night');
    flushSync();
    button(form, 'Save on YouTube')!.click();
    await settle();
    // Only what changed is sent.
    expect(puts).toEqual([{ title: 'Late night' }]);
    expect(panel.textContent).toContain('Changed on YouTube.');
    expect(panel.textContent).toContain('Title: Late night');
  });

  it('says YouTube viewers are unknown rather than 0', async () => {
    engine({
      events: [],
      extra: {
        '/v1/connections/youtube': () => ({
          status: 200,
          body: { chat: { state: 'connected', since: now, broadcast: { id: 'b', title: 'T' }, stream: { viewers: null, title: 'T', description: '' } } },
        }),
      },
    });
    const el = start();
    await settle(120);
    expect(el.textContent).toContain('viewers unknown');
    expect(el.textContent).not.toContain('0 watching');
  });

  it('warns of a raid, locks chat, and bans the raiders only after asking (D-107)', async () => {
    let alert: Record<string, unknown> | null = {
      at: now, reason: 'new_accounts', count: 3, window_seconds: 30, after_raid: 'Raider', locked: false,
      accounts: [
        { platform: 'twitch', id: '1', login: 'bot_a', display_name: 'Bot_A', at: now, age_days: 0 },
        { platform: 'twitch', id: '2', login: 'bot_b', display_name: 'Bot_B', at: now, age_days: 1 },
        { platform: 'youtube', id: 'UCx', login: '', display_name: 'YT person', at: now },
      ],
    };
    const { calls } = engine({
      extra: {
        '/v1/raid-guard': () => ({ status: 200, body: { settings: { mode: 'alert' }, alert } }),
        '/v1/raid-guard/dismiss': () => {
          alert = null;
          return { status: 200, body: { settings: { mode: 'alert' }, alert } };
        },
        '/v1/ban-lists': (_m, body) => ({ status: 200, body: { id: 'bl', ...(body as object) } }),
      },
    });
    const el = start();
    await settle(60);
    const banner = el.querySelector('.live-raid')!;
    expect(banner.textContent).toContain('A burst of new chatters');
    expect(banner.textContent).toContain('A friendly raid needs nothing: just Dismiss. Lock chat or ban only for a hate raid or a flood of bots.');
    expect(banner.textContent).toContain('3 brand-new Twitch accounts in 30 seconds, right after a raid from Raider.');
    expect(banner.textContent).toContain('Bot_A, Bot_B, YT person');
    await noViolations('raid warning');
    // Banning asks first, and bans only the Twitch accounts.
    button(banner, 'Ban these 2 accounts…')!.click();
    flushSync();
    expect(calls.some((c) => c.path === '/v1/ban-lists')).toBe(false);
    button(document.body, 'Ban them')!.click();
    await settle(20);
    const post = calls.find((c) => c.path === '/v1/ban-lists' && c.method === 'POST')!.body as { names: string[]; start: boolean };
    expect(post.names).toEqual(['bot_a', 'bot_b']);
    expect(post.start).toBe(true);
    expect(banner.textContent).toContain('Banning 2 accounts now');
    button(banner, 'Dismiss')!.click();
    await settle(20);
    expect(el.querySelector('.live-raid')).toBeNull();
  });
});
