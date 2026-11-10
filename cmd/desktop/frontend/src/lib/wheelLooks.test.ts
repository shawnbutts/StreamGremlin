// Wheel looks (docs/adr/0036-packages-plugins-and-themes.md): the overlay
// page hosting a look in a sandboxed frame (internal/overlays/page/
// themehost.js), and the built-in looks themselves
// (internal/overlays/themes/), plain JavaScript served by the engine.
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

type Fn = (...a: never[]) => unknown;
let start: (win: unknown, opts?: unknown) => { stop: () => void };
let wheelTheme: (win: unknown, style: unknown, opts?: unknown) => unknown;
let selectorTheme: (win: unknown, opts?: unknown) => unknown;
let workshop: { style: unknown };
let neon: { style: unknown };
let geometry: {
  chances: (c: unknown[]) => string[];
  spans: (c: unknown[]) => [number, number][];
  landing: (c: unknown[], w: number, d: number) => number | null;
};

const root = '../../../../../internal/overlays/';
const load = async (p: string) => (await import(/* @vite-ignore */ new URL(root + p, import.meta.url).pathname)) as Record<string, Fn>;

beforeAll(async () => {
  const w = window as unknown as Record<string, boolean>;
  w.__streamgremlinOverlayTest = true;
  w.__streamgremlinThemeTest = true;
  start = (await load('page/overlay.js')).start as never;
  wheelTheme = (await load('themes/shared/wheeltheme.js')).wheelTheme as never;
  selectorTheme = (await load('themes/selector-board/main.js')).selectorTheme as never;
  workshop = (await load('themes/workshop/main.js')) as never;
  neon = (await load('themes/neon-arcade/main.js')) as never;
  geometry = (await load('themes/shared/wheel.js')) as never;
});

afterEach(() => {
  document.body.innerHTML = '';
  document.documentElement.removeAttribute('style');
  FakeSocket.all = [];
});

function clock() {
  let now = 0;
  let timers: { at: number; fn: () => void }[] = [];
  return {
    now: () => now,
    setTimeout: (fn: () => void, ms: number) => timers.push({ at: now + ms, fn }),
    advance(ms: number) {
      const until = now + ms;
      for (;;) {
        const due = timers.filter((t) => t.at <= until).sort((a, b) => a.at - b.at)[0];
        if (!due) break;
        timers = timers.filter((t) => t !== due);
        now = due.at;
        due.fn();
      }
      now = until;
    },
  };
}

const choices = [
  { label: 'Mario', weight: 2 },
  { label: 'Zelda', weight: 1 },
  { label: 'Tetris', weight: 1, color: '#ff8800' },
];

describe('a built-in wheel look', () => {
  // The look's window: its document, and the overlay page that frames it.
  function look(kind: 'workshop' | 'neon' | 'selector', extra: Record<string, unknown> = {}) {
    document.body.innerHTML = '<main id="app"></main>';
    const sent: { type: string; payload: Record<string, unknown> }[] = [];
    const parent = { postMessage: (m: { type: string; payload: Record<string, unknown> }) => sent.push(m) };
    const listeners: ((ev: { source: unknown; data: unknown }) => void)[] = [];
    const win = { document, parent, addEventListener: (_k: string, fn: (typeof listeners)[0]) => listeners.push(fn), ...extra };
    const c = clock();
    if (kind === 'selector') selectorTheme(win, { setTimeout: c.setTimeout });
    else wheelTheme(win, kind === 'workshop' ? workshop.style : neon.style, { setTimeout: c.setTimeout });
    const post = (type: string, payload: unknown, source: unknown = parent) =>
      listeners.forEach((fn) => fn({ source, data: { protocolVersion: 1, type, payload } }));
    return { sent, post, c, app: document.getElementById('app')! };
  }

  it('says it’s ready, and whether OBS’s page API reached it', () => {
    expect(look('workshop').sent).toEqual([{ protocolVersion: 1, type: 'theme.ready', payload: { apiVersion: '1', obsApi: false } }]);
    expect(look('neon', { obsstudio: {} }).sent[0].payload.obsApi).toBe(true);
  });

  it('draws wedges sized to their weights and spins to the drawn number, labels as text', () => {
    for (const kind of ['workshop', 'neon'] as const) {
      const { sent, post, c, app } = look(kind);
      post('session.snapshot', { settings: { title: '', show_title: true, accent: '#123456' } });
      const named = [...choices, { label: '<img src=x onerror=alert(1)>', weight: 4 }];
      post('spin.start', { spinId: 's1', title: 'Next game', choices: named, winner: 0, draw: 1, durationMs: 6000 });
      const wedges = [...app.querySelectorAll('path.wedge')];
      expect(wedges.length).toBe(4);
      // Each wedge's arc matches its share: Mario 2 of 8 (90°), Zelda and Tetris 45° each, the last 180°.
      expect(geometry.spans(named)).toEqual([
        [0, 90],
        [90, 135],
        [135, 180],
        [180, 360],
      ]);
      expect(app.querySelector('img')).toBeNull();
      expect(app.textContent).toContain('Next game');
      expect(document.documentElement.style.getPropertyValue('--accent')).toBe('#123456');
      // Number 1 is the middle of Mario's second slot: 67.5° from the top.
      const rotor = app.querySelector('.rotor') as SVGGElement;
      expect(rotor.style.transform).toBe(`rotate(${5 * 360 + 360 - 67.5}deg)`);
      expect(sent.map((m) => m.type)).toEqual(['theme.ready']);
      c.advance(6000);
      expect(sent.at(-1)).toEqual({ protocolVersion: 1, type: 'spin.animation.completed', payload: { spinId: 's1' } });
      // The result only when the page says, and only for this spin.
      post('winner.reveal', { spinId: 'other', label: 'Zelda' });
      expect(app.querySelector('.result')!.hasAttribute('hidden')).toBe(true);
      post('winner.reveal', { spinId: 's1', label: 'Mario' });
      expect(app.querySelector('.result')!.textContent).toBe('🎉 Mario');
      post('spin.cancel', {});
      expect(app.querySelector('path')).toBeNull();
    }
  });

  it('refuses a spin that doesn’t add up, and ignores anyone but the page that frames it', () => {
    const { sent, post, app } = look('workshop');
    post('spin.start', { spinId: 'bad', choices, winner: 1, draw: 0, durationMs: 6000 }); // 0 is Mario's
    expect(sent.at(-1)).toEqual({ protocolVersion: 1, type: 'theme.error', payload: { spinId: 'bad', code: 'bad_spin' } });
    expect(app.querySelector('path')).toBeNull();
    post('spin.start', { spinId: 's1', choices, winner: 1, draw: 2, durationMs: 6000 }, { someone: 'else' });
    expect(app.querySelector('path')).toBeNull();
  });

  it('with reduced motion, shows the wheel stopped at once', () => {
    const { sent, post, app } = look('neon');
    post('session.snapshot', { settings: {}, reducedMotion: true });
    post('spin.start', { spinId: 's1', choices, winner: 2, draw: 3, durationMs: 0 });
    expect((app.querySelector('.rotor') as SVGGElement).style.transition).toBe('');
    expect(sent.at(-1)!.type).toBe('spin.animation.completed');
  });

  it('the selector board shows each choice’s chance and stops on the winner', () => {
    const { sent, post, c, app } = look('selector');
    post('session.snapshot', { settings: { title: 'Pick one', show_title: true } });
    post('spin.start', { spinId: 's1', title: 'Wheel', choices, winner: 1, draw: 2, durationMs: 4000 });
    expect(app.querySelector('.title')!.textContent).toBe('Pick one');
    const rows = [...app.querySelectorAll('.row')];
    expect(rows.slice(0, 3).map((r) => r.textContent)).toEqual(['Mario50%', 'Zelda25%', 'Tetris25%']);
    const current = app.querySelectorAll('.row.current');
    expect(current.length).toBe(1);
    expect(current[0].textContent).toBe('Zelda25%');
    expect(rows.indexOf(current[0])).toBe(4 * 3 + 1); // the last pass
    c.advance(4000);
    expect(sent.at(-1)!.type).toBe('spin.animation.completed');
  });

  it('works out chances as people read them', () => {
    const w = (ns: number[]) => ns.map((weight) => ({ label: 'x', weight }));
    expect(geometry.chances(w([2, 1, 2, 1, 1]))).toEqual(['29%', '14%', '29%', '14%', '14%']);
    expect(geometry.chances(w([1, 11]))).toEqual(['8.3%', '92%']);
    expect(geometry.chances(w([1, 1000]))).toEqual(['<0.1%', '100%']);
    expect(geometry.landing(w([1, 1]), 1, 1)).toBe(270);
    expect(geometry.landing(w([1, 1]), 1, 0)).toBeNull();
  });
});

class FakeSocket {
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, string>[] = [];
  listeners: Record<string, ((ev: { data?: string }) => void)[]> = {};
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  addEventListener(kind: string, fn: (ev: { data?: string }) => void) {
    (this.listeners[kind] ??= []).push(fn);
  }
  send(s: string) {
    this.sent.push(JSON.parse(s));
  }
  close() {}
  fire(kind: string, data?: unknown) {
    if (kind === 'open') this.readyState = 1;
    for (const fn of this.listeners[kind] ?? []) fn({ data: data === undefined ? undefined : JSON.stringify(data) });
  }
}

describe('the overlay page with a wheel look', () => {
  const url = 'http://127.0.0.1:50123/t/builtin/workshop/v1/index.html';
  const theme = { id: 'builtin:workshop', name: 'Workshop', url, settings: { title: '', show_title: true, accent: '#e8b04e' }, odds_checked: true };
  const look = { start_sound: 'drumroll', end_sound: 'clunk', cheer_sound: 'cheer' };
  const hello = (t: unknown = theme) => ({ type: 'hello', body: { overlay: { name: 'wheel', kind: 'wheel', look }, theme: t, page_version: 'v1' } });
  const spin = (id: string, winner = 0, draw = 1) => ({ type: 'spin', body: { id, title: 'Next game', choices, winner, draw, seconds: 6 } });

  function page(search = '?status=1') {
    document.body.innerHTML = '<div id="status" hidden></div><div id="stage"></div>';
    const c = clock();
    const sounds: string[] = [];
    const win = {
      document,
      location: { search, pathname: '/o/tok/', protocol: 'http:', host: '127.0.0.1:7431' },
      addEventListener: (k: string, fn: EventListener) => window.addEventListener(k, fn),
      removeEventListener: (k: string, fn: EventListener) => window.removeEventListener(k, fn),
    };
    const ov = start(win, {
      WebSocket: FakeSocket,
      setTimeout: c.setTimeout,
      now: c.now,
      Audio: (src: string) => ({ volume: 1, play: () => (sounds.push(src.split('/').at(-1)!), Promise.resolve()) }),
    });
    const ws = FakeSocket.all.at(-1)!;
    ws.fire('open');
    ws.fire('message', hello());
    const frame = document.querySelector('iframe')!;
    const posted: { type: string; payload: Record<string, unknown> }[] = [];
    if (frame) (frame.contentWindow as unknown as { postMessage: unknown }).postMessage = (m: (typeof posted)[0]) => posted.push(m);
    const fromLook = (type: string, payload: unknown = {}, source: unknown = frame.contentWindow, extra: Record<string, unknown> = {}) =>
      window.dispatchEvent(new MessageEvent('message', { source: source as Window, data: { protocolVersion: 1, type, payload, ...extra } }));
    return { ov, ws, c, frame, posted, fromLook, sounds, stage: document.getElementById('stage')!, status: document.getElementById('status')! };
  }

  it('frames the look sandboxed, with no referrer, from the theme server only', () => {
    const { frame, ov } = page();
    expect(frame.getAttribute('sandbox')).toBe('allow-scripts');
    expect(frame.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(frame.src).toBe(url);
    ov.stop();
    // A link anywhere else is never framed: Classic instead.
    const p = page();
    p.ws.fire('message', hello({ ...theme, url: 'https://evil.example/t/x' }));
    expect(document.querySelector('iframe')).toBeNull();
  });

  it('plays a spin through the look on the engine’s clock, with the sounds, then hides it', () => {
    const { ws, c, frame, posted, fromLook, sounds, status } = page();
    fromLook('theme.ready', { apiVersion: '1', obsApi: false });
    expect(posted.at(-1)).toMatchObject({ type: 'session.snapshot', payload: { settings: theme.settings, reducedMotion: false } });
    expect(status.textContent).toBe('Look: Workshop ready; OBS page API not reachable');
    expect(ws.sent.at(-1)).toEqual({ type: 'theme', status: 'ready', detail: 'Workshop ready; OBS page API not reachable' });
    ws.fire('message', spin('s1'));
    expect(posted.at(-1)).toMatchObject({ type: 'spin.start', payload: { spinId: 's1', winner: 0, draw: 1, durationMs: 6000, title: 'Next game' } });
    expect(frame.classList.contains('on')).toBe(true);
    expect(sounds).toEqual(['drumroll']);
    // The look says it finished early: nothing changes. The engine's clock decides.
    fromLook('spin.animation.completed', { spinId: 's1' });
    c.advance(6000);
    expect(sounds).toEqual(['drumroll', 'clunk']);
    expect(posted.some((m) => m.type === 'winner.reveal')).toBe(false);
    c.advance(500);
    expect(sounds).toEqual(['drumroll', 'clunk', 'cheer']);
    expect(posted.at(-1)).toEqual({ protocolVersion: 1, type: 'winner.reveal', payload: { spinId: 's1', label: 'Mario' } });
    ws.fire('message', spin('s2', 1, 2)); // waits its turn
    expect(posted.filter((m) => m.type === 'spin.start').length).toBe(1);
    c.advance(4000);
    expect(posted.filter((m) => m.type === 'spin.cancel').length).toBe(1);
    expect(posted.at(-1)).toMatchObject({ type: 'spin.start', payload: { spinId: 's2', winner: 1 } });
  });

  it('shows a spin in Classic when the look isn’t ready, or fails, and never shows a spin that doesn’t add up', () => {
    const { ws, c, stage, frame, posted, fromLook } = page();
    ws.fire('message', spin('s1'));
    expect(stage.querySelector('.sg-wheel')).not.toBeNull(); // Classic
    expect(frame.classList.contains('on')).toBe(false);
    expect(ws.sent.at(-1)).toMatchObject({ type: 'theme', status: 'fallback' });
    c.advance(12000);
    fromLook('theme.ready', {});
    ws.fire('message', spin('bad', 1, 0));
    expect(posted.some((m) => m.type === 'spin.start')).toBe(false);
    ws.fire('message', spin('s2', 2, 3));
    expect(frame.classList.contains('on')).toBe(true);
    // An error about another spin changes nothing; about this one, Classic shows it, stopped.
    fromLook('theme.error', { spinId: 'other', code: 'x' });
    expect(frame.classList.contains('on')).toBe(true);
    fromLook('theme.error', { spinId: 's2', code: 'bad_spin' });
    expect(frame.classList.contains('on')).toBe(false);
    expect(posted.at(-1)!.type).toBe('spin.cancel');
    expect(stage.textContent).toContain('🎉 Tetris');
    const before = posted.length;
    c.advance(20000); // the look's own steps for s2 no longer happen
    expect(posted.slice(before).some((m) => m.type === 'winner.reveal')).toBe(false);
  });

  it('drops what the look may not send: other windows, other kinds, too much, too often', () => {
    const { ws, posted, fromLook } = page();
    fromLook('theme.ready', {}, window);
    fromLook('theme.ready', {}, undefined, { protocolVersion: 2 });
    fromLook('spin.start', { spinId: 'x' });
    fromLook('theme.ready', { pad: 'x'.repeat(20000) });
    expect(posted.length).toBe(0);
    for (let i = 0; i < 25; i++) fromLook('spin.animation.completed', { spinId: 'x' });
    fromLook('theme.ready', {}); // the 26th this second: dropped
    expect(posted.length).toBe(0);
    expect(ws.sent.some((m) => m.status === 'ready')).toBe(false);
  });

  it('changes settings in place, and goes back to Classic', () => {
    const { ws, posted, fromLook, frame } = page();
    fromLook('theme.ready', {});
    ws.fire('message', hello({ ...theme, settings: { ...theme.settings, title: 'Raid wheel' } }));
    expect(posted.at(-1)).toMatchObject({ type: 'theme.settings.updated', payload: { settings: { title: 'Raid wheel' } } });
    expect(document.querySelector('iframe')).toBe(frame);
    ws.fire('message', hello(null));
    expect(document.querySelector('iframe')).toBeNull();
    ws.fire('message', spin('s9', 2, 3));
    expect(document.querySelector('.sg-wheel')).not.toBeNull();
  });
});

vi.setConfig({ testTimeout: 10000 });
