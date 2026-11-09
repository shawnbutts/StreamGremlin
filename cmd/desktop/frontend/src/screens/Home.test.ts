import axe from 'axe-core';
import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import Home from './Home.svelte';
import { mockFetch, obsOff, runningStatus, settle, twitchStatus } from '../test/helpers';
import type { Overview } from '../lib/api';

let comp: ReturnType<typeof mount> | undefined;
let went: string[] = [];

beforeEach(() => {
  went = [];
  localStorage.clear();
});

afterEach(() => {
  if (comp) unmount(comp);
  comp = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function start() {
  const target = document.createElement('main');
  document.body.appendChild(target);
  comp = mount(Home, { target, props: { refreshMs: 30, idleMs: 20, retryMs: 20, ongo: (s: string) => went.push(s) } });
  return target;
}

const button = (el: ParentNode, text: string) =>
  Array.from(el.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim() === text);

const step = (id: string, done: boolean, optional = false) => ({
  id, label: `Step ${id}`, help: `Help for ${id}`, done, optional, screen: id === 'workflow' ? 'workflows' : 'connections',
});

const overview = (over: Partial<Overview> = {}): Overview => ({
  checklist: [step('twitch', true), step('bot', false, true), step('workflow', false), step('test', false)],
  set_up: false,
  attention: [],
  workflows: { total: 0, on: 0, tested: false, answered: false, failed_24h: 0 },
  ...over,
});

function engine(ov: Overview, events: unknown[] = []) {
  return mockFetch({
    '/v1/status': runningStatus,
    '/v1/overview': () => ({ status: 200, body: ov }),
    '/v1/connections': () =>
      twitchStatus({
        streamer: { state: 'connected', login: 'streamer' },
        bot: { state: 'unknown', login: 'gremlinbot', detail: 'Twitch didn’t answer.' },
        chat: { state: 'connected', since: new Date().toISOString() },
      }),
    '/v1/jobs': () => ({
      status: 200,
      body: { jobs: [{ id: 'j1', workflow_id: 'chat.hello', version: 1, event_id: 'e', event_seq: 1, status: 'failed', dry_run: false, created_at: new Date().toISOString() }] },
    }),
    '/v1/workflows': () => ({ status: 200, body: { workflows: [{ id: 'chat.hello', name: 'Say hello', enabled: true, version: 1, updated_at: '', triggers: [] }] } }),
    '/v1/events': (_m, _b, q) => ({ status: 200, body: { events: q.includes('after') ? [] : events, head: events.length } }),
  });
}

async function noViolations(label: string) {
  for (const theme of ['dark', 'light', 'midnight']) {
    document.documentElement.dataset.theme = theme;
    const r = await axe.run(document.body, { rules: { 'color-contrast': { enabled: false } } });
    expect(r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(', ')}`), `${label}, ${theme}`).toEqual([]);
  }
}

describe('Home', () => {
  it('shows the checklist from real state, the next step, and goes where it says', async () => {
    engine(overview());
    const el = start();
    await settle(60);
    const setup = el.querySelector('section[aria-labelledby="setup-heading"]')!;
    expect(setup.textContent).toContain('1 of 3 done'); // optional steps don't count
    expect(setup.textContent).toContain('Step bot (optional)');
    expect(setup.textContent).toContain('Help for workflow');
    expect(setup.textContent).not.toContain('Help for twitch'); // done steps keep quiet
    // The first step not done is the one to do next.
    const next = setup.querySelector('.step-next')!;
    expect(next.textContent).toContain('Step workflow');
    next.querySelector('button')!.click();
    expect(went).toEqual(['workflows']);
    expect(el.textContent).toContain('Nothing needs you right now.');
    await noViolations('home');
  });

  it('hides the checklist when asked, remembers it, and can show it again', async () => {
    engine(overview());
    let el = start();
    await settle(60);
    button(el, 'Hide the checklist')!.click();
    flushSync();
    expect(el.querySelector('section[aria-labelledby="setup-heading"]')).toBeNull();
    unmount(comp!);
    document.body.innerHTML = '';
    el = start();
    await settle(60);
    expect(el.querySelector('section[aria-labelledby="setup-heading"]')).toBeNull();
    button(el, 'Show the Get set up checklist')!.click();
    flushSync();
    expect(el.querySelector('section[aria-labelledby="setup-heading"]')).not.toBeNull();
  });

  it('shows what needs you, health with unknown as unknown, and what happened', async () => {
    engine(
      overview({
        set_up: true,
        attention: [
          { level: 'problem', message: 'OBS needs you: OBS refused the password.', screen: 'connections' },
          { level: 'unknown', message: 'Can’t check the bot account right now.', screen: 'connections' },
          { level: 'tip', message: 'Make gremlinbot a moderator.', screen: 'connections' },
        ],
      }),
      [{ seq: 1, event_id: 'e1', type: 'chat.message.received', source: 'twitch', received_at: new Date().toISOString(), occurred_at: '', delivery_quality: 'synthetic', actor: { platform: 'twitch', id: '1', display_name: 'Fan' }, payload: { text: 'hi there' } }],
    );
    const el = start();
    await settle(80);
    expect(el.querySelector('section[aria-labelledby="setup-heading"]')).toBeNull(); // set up: no checklist
    const att = el.querySelector('.attention')!;
    expect(att.textContent).toContain('Problem: OBS needs you');
    expect(att.textContent).toContain('Unknown: Can’t check the bot account');
    button(att, 'Open Connections')!.click();
    expect(went).toEqual(['connections']);
    const conn = el.querySelector('section[aria-labelledby="conn-heading"]')!.textContent!;
    expect(conn).toContain('Signed in as streamer');
    expect(conn).toContain('Unknown (signed in as gremlinbot): Twitch didn’t answer');
    expect(conn).toContain('Reading your chat');
    expect(conn).toContain('Off'); // OBS
    const live = el.querySelector('section[aria-labelledby="live-heading"]')!.textContent!;
    expect(live).toContain('Fan: hi there');
    expect(live).toContain('Test');
    expect(live).toContain('Say hello: Failed');
    await noViolations('attention');
  });

  it('marks failed runs as seen, and the notice goes', async () => {
    let seen = false;
    const failed = { level: 'problem' as const, message: '3 workflow runs failed in the last 24 hours. Their traces say why.', screen: 'workflows', action: 'mark_failures_seen' as const };
    const calls = engine(overview({ set_up: true, attention: [failed] }));
    const inner = (globalThis.fetch as unknown as { getMockImplementation: () => (p: string, i?: RequestInit) => Promise<Response> }).getMockImplementation();
    vi.stubGlobal('fetch', vi.fn(async (p: string, i?: RequestInit) => {
      if (p === '/v1/overview/failures-seen') {
        seen = true;
        calls.push({ method: 'POST', path: p, body: undefined });
        return new Response(JSON.stringify({ seen_at: new Date().toISOString() }), { status: 200 });
      }
      if (p === '/v1/overview') return new Response(JSON.stringify(overview({ set_up: true, attention: seen ? [] : [failed] })), { status: 200 });
      return inner(p, i);
    }));
    const el = start();
    await settle(80);
    const att = el.querySelector('.attention')!;
    expect(att.textContent).toContain('3 workflow runs failed');
    await noViolations('failures');
    button(att, 'Mark as seen')!.click();
    await settle(80);
    expect(calls.some((c) => c.path === '/v1/overview/failures-seen')).toBe(true);
    expect(el.textContent).not.toContain('3 workflow runs failed');
  });

  it('shows everything as unknown when the engine stops answering', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('down'); }));
    const el = start();
    await settle(60);
    expect(el.textContent).not.toContain('Running');
    expect(el.textContent).not.toContain('Signed in');
    expect(el.textContent).not.toContain('Nothing needs you');
    expect(el.querySelector('section[aria-labelledby="conn-heading"]')!.textContent).toContain('Unknown');
  });
});
