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
let paperWheel: (win: unknown, opts?: unknown) => void;
let hostile: (win: unknown, opts?: unknown) => { tries: [string, string][] };
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
  paperWheel = (await load('../../examples/themes/paper-wheel/wheel.js')).paperWheel as never;
  hostile = (await load('../../tests/testdata/hostile-wheel/hostile.js')).hostile as never;
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
    // With the level OBS gives the frame (D-067), or none if it doesn't answer.
    const obsstudio = { getControlLevel: (cb: (n: number) => void) => cb(1) };
    expect(look('workshop', { obsstudio }).sent[0].payload).toEqual({ apiVersion: '1', obsApi: true, obsLevel: 1 });
    const silent = look('selector', { obsstudio: { getControlLevel: () => {} } });
    expect(silent.sent).toEqual([]);
    silent.c.advance(1000);
    expect(silent.sent[0].payload).toEqual({ apiVersion: '1', obsApi: true, obsLevel: null });
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

describe('the example look, Paper wheel', () => {
  it('spins: the wheel is on screen before its turn is set, so the browser animates it', async () => {
    const page = new URL(root + '../../examples/themes/paper-wheel/index.html', import.meta.url).pathname;
    const html = (await import('node:fs')).readFileSync(page, 'utf8');
    document.body.innerHTML = html.slice(html.indexOf('<main'), html.indexOf('</main>') + 7);
    const sent: { type: string; payload: Record<string, unknown> }[] = [];
    const parent = { postMessage: (m: { type: string; payload: Record<string, unknown> }) => sent.push(m) };
    const listeners: ((ev: { source: unknown; data: unknown }) => void)[] = [];
    const c = clock();
    const win = { document, parent, addEventListener: (_k: string, fn: (typeof listeners)[0]) => listeners.push(fn) };
    paperWheel(win, { setTimeout: c.setTimeout });
    const post = (type: string, payload: unknown) =>
      listeners.forEach((fn) => fn({ source: parent, data: { protocolVersion: 1, type, payload } }));
    expect(sent[0].type).toBe('theme.ready');
    const stage = document.getElementById('stage')!;
    const rotor = document.getElementById('rotor')!;
    // A browser only animates what it has laid out: was the wheel showing
    // when the look made the page lay out its starting turn?
    let shownAtReset: boolean | undefined;
    rotor.getBoundingClientRect = () => {
      shownAtReset = !stage.hidden;
      return new DOMRect();
    };
    post('session.snapshot', { settings: { paper: 'Sky' } });
    post('spin.start', { spinId: 's1', title: 'Next game', choices, winner: 0, draw: 1, durationMs: 6000 });
    expect(shownAtReset).toBe(true);
    expect(rotor.style.transition).toContain('6000ms');
    expect(rotor.style.transform).toBe(`rotate(${5 * 360 + 360 - 135}deg)`); // number 1 of 4 is at 135°
    expect(document.getElementById('chances')!.textContent).toContain('Mario: 50%');
    c.advance(6000);
    expect(sent.at(-1)).toEqual({ protocolVersion: 1, type: 'spin.animation.completed', payload: { spinId: 's1' } });
    post('spin.start', { spinId: 'bad', choices, winner: 1, draw: 0, durationMs: 6000 });
    expect(sent.at(-1)!.type).toBe('theme.error');
  });
});

describe('the overlay page with a wheel look', () => {
  const url = 'http://127.0.0.1:50123/t/builtin/workshop/v1/index.html';
  const theme = { id: 'builtin:workshop', name: 'Workshop', url, settings: { title: '', show_title: true, accent: '#e8b04e' }, odds_checked: true };
  const look = { start_sound: 'drumroll', end_sound: 'clunk', cheer_sound: 'cheer' };
  const hello = (t: unknown = theme, lk: unknown = look) => ({ type: 'hello', body: { overlay: { name: 'wheel', kind: 'wheel', look: lk }, theme: t, page_version: 'v1' } });
  const spin = (id: string, winner = 0, draw = 1) => ({ type: 'spin', body: { id, title: 'Next game', choices, winner, draw, seconds: 6 } });

  function page(search = '?status=1', first: ReturnType<typeof hello> = hello(), obsstudio?: unknown) {
    document.body.innerHTML = '<div id="status" hidden></div><div id="stage"></div>';
    const c = clock();
    const sounds: string[] = [];
    const played: { src: string; volume: number }[] = [];
    const win = {
      ...(obsstudio === undefined ? {} : { obsstudio }),
      document,
      location: { search, pathname: '/o/tok/', protocol: 'http:', host: '127.0.0.1:7431' },
      addEventListener: (k: string, fn: EventListener) => window.addEventListener(k, fn),
      removeEventListener: (k: string, fn: EventListener) => window.removeEventListener(k, fn),
    };
    const ov = start(win, {
      WebSocket: FakeSocket,
      setTimeout: c.setTimeout,
      now: c.now,
      Audio: (src: string) => ({
        volume: 1,
        play() {
          sounds.push(src.split('/').at(-1)!);
          played.push({ src, volume: this.volume });
          return Promise.resolve();
        },
      }),
    });
    const ws = FakeSocket.all.at(-1)!;
    ws.fire('open');
    ws.fire('message', first);
    const frame = document.querySelector('iframe')!;
    const posted: { type: string; payload: Record<string, unknown> }[] = [];
    if (frame) (frame.contentWindow as unknown as { postMessage: unknown }).postMessage = (m: (typeof posted)[0]) => posted.push(m);
    const fromLook = (type: string, payload: unknown = {}, source: unknown = frame.contentWindow, extra: Record<string, unknown> = {}) =>
      window.dispatchEvent(new MessageEvent('message', { source: source as Window, data: { protocolVersion: 1, type, payload, ...extra } }));
    return { ov, ws, c, frame, posted, fromLook, sounds, played, stage: document.getElementById('stage')!, status: document.getElementById('status')! };
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

  // D-067: OBS's page API reaches the look's sandboxed frame, with the
  // browser source's Page permissions. An add-on look is someone else's
  // code, so it's only framed at level 0, "No access to OBS".
  describe('an add-on look and OBS’s Page permissions', () => {
    const addOnUrl = `http://127.0.0.1:50123/t/paper-wheel/1.1.0/${'ab'.repeat(32)}/index.html`;
    const addOn = {
      id: 'paper-wheel', name: 'Paper wheel', url: addOnUrl, settings: {}, odds_checked: false, add_on: true,
      sounds: { start: addOnUrl.replace('index.html', 'start.wav') },
    };
    const noSounds = { start_sound: '', end_sound: '', cheer_sound: '' };
    const level = (n: unknown) => ({ getControlLevel: (cb: (l: unknown) => void) => cb(n) });

    it('loads outside OBS, and in OBS at “No access to OBS”', () => {
      expect(page('?status=1', hello(addOn)).frame.src).toBe(addOnUrl);
      expect(page('?status=1', hello(addOn), level(0)).frame.src).toBe(addOnUrl);
    });

    it('above it, shows Classic, without the look’s sounds, and says why once', () => {
      const { ws, c, stage, status, sounds } = page('?status=1', hello(addOn, noSounds), level(4));
      expect(document.querySelector('iframe')).toBeNull();
      const why = 'Paper wheel not loaded: this browser source’s Page permissions are “Advanced access to OBS”. Add-on looks only load at “No access to OBS”; Classic shows instead';
      expect(ws.sent.filter((m) => m.type === 'theme')).toEqual([{ type: 'theme', status: 'refused', detail: why }]);
      expect(status.textContent).toBe(`Look: ${why}`);
      ws.fire('message', hello(addOn, noSounds)); // said once, not on every hello
      expect(ws.sent.filter((m) => m.type === 'theme').length).toBe(1);
      ws.fire('message', spin('s1'));
      expect(stage.querySelector('.sg-wheel')).not.toBeNull();
      c.advance(10000);
      expect(sounds).toEqual([]);
      // The built-in looks are StreamGremlin's own, and never call OBS: they still load.
      ws.fire('message', hello());
      expect(document.querySelector('iframe')!.src).toBe(url);
    });

    it('a look anywhere but the built-in path is an add-on, flag or not', () => {
      page('?status=1', hello({ ...addOn, add_on: undefined }), level(1));
      expect(document.querySelector('iframe')).toBeNull();
    });

    it('waits for OBS’s answer, and treats no answer, or an OBS that can’t say, as not allowed', () => {
      let answer: (l: unknown) => void = () => {};
      const p = page('?status=1', hello(addOn), { getControlLevel: (cb: (l: unknown) => void) => (answer = cb) });
      expect(document.querySelector('iframe')).toBeNull();
      expect(p.ws.sent.some((m) => m.type === 'theme')).toBe(false); // still asking
      answer(0);
      expect(document.querySelector('iframe')!.src).toBe(addOnUrl);
      p.ov.stop();
      const q = page('?status=1', hello(addOn), { getControlLevel: () => {} });
      q.c.advance(2000);
      expect(document.querySelector('iframe')).toBeNull();
      expect(q.ws.sent.at(-1)).toMatchObject({ type: 'theme', status: 'refused', detail: expect.stringContaining('OBS didn’t say') });
      q.ov.stop();
      const r = page('?status=1', hello(addOn), {}); // an OBS without control levels
      expect(document.querySelector('iframe')).toBeNull();
      expect(r.ws.sent.at(-1)).toMatchObject({ type: 'theme', status: 'refused' });
    });

    it('a look says the level OBS gives its frame, and the status line names it', () => {
      const { fromLook, status } = page();
      fromLook('theme.ready', { apiVersion: '1', obsApi: true, obsLevel: 1 });
      expect(status.textContent).toBe('Look: Workshop ready; OBS page API reachable, at level 1 (Read access to OBS status information)');
      // What OBS does in a sandboxed frame: the object is there, the answer never comes.
      const q = page();
      q.fromLook('theme.ready', { apiVersion: '1', obsApi: true, obsLevel: null });
      expect(q.status.textContent).toBe('Look: Workshop ready; OBS page API reachable; OBS didn’t tell the look its level');
    });
  });

  it('a new version of the look mid-spin: that spin finishes first, and the next is in the new version', () => {
    const { ws, c, frame, fromLook, posted } = page();
    fromLook('theme.ready', {});
    ws.fire('message', spin('s1'));
    const v2 = url.replace('/v1/', '/v2/');
    ws.fire('message', hello({ ...theme, url: v2 }));
    expect(document.querySelectorAll('iframe').length).toBe(1);
    expect(document.querySelector('iframe')).toBe(frame); // still the old one, mid-spin
    ws.fire('message', spin('s2', 1, 2)); // waits its turn
    c.advance(6500);
    expect(posted.some((m) => m.type === 'winner.reveal')).toBe(true);
    expect(document.querySelector('iframe')).toBe(frame);
    c.advance(4000); // s1 done: the new version, with s2 carried over
    const now = document.querySelector('iframe')!;
    expect(now).not.toBe(frame);
    expect(now.src).toBe(v2);
    expect(document.querySelectorAll('iframe').length).toBe(1);
    expect(ws.sent.at(-1)).toMatchObject({ type: 'theme', status: 'fallback' }); // s2, before the new one is ready
    // Not mid-spin: at once.
    c.advance(20000);
    ws.fire('message', hello({ ...theme, url }));
    expect(document.querySelector('iframe')!.src).toBe(url);
  });

  // D-068: the hostile test look's own misbehaviour, against the real
  // page. (What the sandbox blocks is checked in a browser: the live check.)
  it('a hostile look can’t finish a spin early, end others’ spins, reveal a result, or flood the page', () => {
    const hostileUrl = `http://127.0.0.1:50123/t/hostile-wheel/1.0.0/${'ab'.repeat(32)}/index.html`;
    const { ws, c, frame, posted, sounds } = page('?status=1', hello({ id: 'hostile-wheel', name: 'Hostile wheel (test)', url: hostileUrl, settings: {}, odds_checked: false, add_on: true }));
    // The look's window: its own document; what it sends arrives as the frame's.
    const lookDoc = document.implementation.createHTMLDocument('look');
    lookDoc.body.innerHTML = '<p id="claim"></p><ul id="tries"></ul>';
    const listeners: ((ev: { source: unknown; data: unknown }) => void)[] = [];
    const parent = {
      postMessage: (m: unknown) => window.dispatchEvent(new MessageEvent('message', { source: frame.contentWindow as Window, data: m })),
    };
    hostile({ document: lookDoc, parent, addEventListener: (_k: string, fn: (typeof listeners)[0]) => listeners.push(fn) }, { setTimeout: c.setTimeout, probe: false });
    // What the page sends the look reaches it.
    (frame.contentWindow as unknown as { postMessage: unknown }).postMessage = (m: { type: string; payload: Record<string, unknown> }) => {
      posted.push(m);
      listeners.forEach((fn) => fn({ source: parent, data: m }));
    };
    expect(ws.sent.at(-1)).toMatchObject({ type: 'theme', status: 'ready' });
    ws.fire('message', spin('s1'));
    ws.fire('message', spin('s2', 1, 2));
    // It said it finished, flooded, and claimed a reveal: nothing moved.
    expect(frame.classList.contains('on')).toBe(true);
    expect(posted.filter((m) => m.type === 'winner.reveal')).toEqual([]);
    expect(posted.filter((m) => m.type === 'spin.start').length).toBe(1);
    expect(sounds).toEqual(['drumroll']);
    c.advance(6500); // on the engine's clock: the real result
    expect(posted.filter((m) => m.type === 'winner.reveal')).toEqual([{ protocolVersion: 1, type: 'winner.reveal', payload: { spinId: 's1', label: 'Mario' } }]);
    expect(sounds).toEqual(['drumroll', 'clunk', 'cheer']);
    // What it draws is its own: a made-up winner. Chat has the real one.
    expect(lookDoc.getElementById('claim')!.textContent).toContain('Not the real winner');
    c.advance(4000);
    expect(posted.filter((m) => m.type === 'spin.start').map((m) => m.payload.spinId)).toEqual(['s1', 's2']);
    expect(ws.sent.filter((m) => m.type === 'theme' && m.status !== 'ready')).toEqual([]);
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
  it('plays the look’s own sounds for cues the streamer left empty, at their volumes, and only from the theme server', () => {
    const base = 'http://127.0.0.1:50123/t/builtin/v1/workshop/';
    const sounds = { start: `${base}start.wav`, end: `${base}end.wav`, cheer: `${base}cheer.wav` };
    const own = { start_sound: '', start_volume: 30, end_sound: '', end_volume: 80, cheer_sound: 'cheer', cheer_volume: 60 };
    const { ws, c, fromLook, played } = page('?status=1', hello({ ...theme, sounds }, own));
    fromLook('theme.ready', {});
    ws.fire('message', spin('s1'));
    c.advance(6500);
    expect(played).toEqual([
      { src: sounds.start, volume: 0.3 },
      { src: sounds.end, volume: 0.8 },
      { src: '/o/tok/sound/cheer', volume: 0.6 }, // the streamer's own wins
    ]);
    // Sounds from anywhere but the theme server, or not .wav, never play.
    c.advance(4000);
    ws.fire('message', hello({ ...theme, sounds: { start: 'https://evil.example/t/a.wav', end: `${base}end.js` } }, own));
    ws.fire('message', spin('s2'));
    c.advance(6500);
    expect(played.length).toBe(4); // only the cheer
    // The look's sounds turned off: none sent, none played.
    c.advance(4000);
    ws.fire('message', hello(theme, own));
    ws.fire('message', spin('s3'));
    c.advance(6500);
    expect(played.length).toBe(5);
  });

  it('keeps the app’s preview silent, look sounds too', () => {
    const base = 'http://127.0.0.1:50123/t/builtin/v1/workshop/';
    const { ws, c, fromLook, played } = page('?preview=1', hello({ ...theme, sounds: { start: `${base}start.wav` } }, {}));
    fromLook('theme.ready', {});
    ws.fire('message', spin('s1'));
    c.advance(12000);
    expect(played).toEqual([]);
  });
});

vi.setConfig({ testTimeout: 10000 });
