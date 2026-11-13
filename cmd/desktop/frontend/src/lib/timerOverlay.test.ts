// Clock and timer overlays (D-092): the overlay page's Classic clock,
// countdown and stopwatch (internal/overlays/page/timer.js), and a look in
// a sandboxed frame (timerhost.js). Plain JavaScript served by the engine.
import { afterEach, beforeAll, describe, expect, it } from 'vitest';

let start: (win: unknown, opts?: unknown) => { stop: () => void };
let duration: (ms: number) => string;

const root = '../../../../../internal/overlays/';
const load = async (p: string) => (await import(/* @vite-ignore */ new URL(root + p, import.meta.url).pathname)) as Record<string, unknown>;

beforeAll(async () => {
  (window as unknown as Record<string, boolean>).__streamgremlinOverlayTest = true;
  start = (await load('page/overlay.js')).start as never;
  duration = (await load('page/timer.js')).duration as never;
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
  close() {
    this.fire('close');
  }
  fire(kind: string, data?: unknown) {
    if (kind === 'open') this.readyState = 1;
    for (const fn of this.listeners[kind] ?? []) fn({ data: data === undefined ? undefined : JSON.stringify(data) });
  }
}

// A clock the test moves by hand, starting at 18:30 today.
function clock() {
  let now = new Date(2026, 10, 13, 18, 30, 0).getTime();
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

const hello = (look: Record<string, unknown>, theme?: unknown) => ({
  type: 'hello',
  body: { overlay: { name: 'timer', kind: 'timer', look: { animation: 'slide', ...look } }, theme, page_version: 'v1' },
});

function page(first: unknown) {
  document.body.innerHTML = '<div id="status" hidden></div><div id="stage"></div>';
  const c = clock();
  const win = {
    document,
    location: { search: '?status=1', pathname: '/o/tok/', protocol: 'http:', host: '127.0.0.1:7431' },
    addEventListener: (k: string, fn: EventListener) => window.addEventListener(k, fn),
    removeEventListener: (k: string, fn: EventListener) => window.removeEventListener(k, fn),
  };
  const ov = start(win, { WebSocket: FakeSocket, setTimeout: c.setTimeout, now: c.now });
  const ws = FakeSocket.all.at(-1)!;
  ws.fire('open');
  ws.fire('message', first);
  const stage = document.getElementById('stage')!;
  const digits = () => stage.querySelector('.sg-timer-digits')?.textContent;
  return { ov, ws, c, stage, digits };
}

afterEach(() => {
  FakeSocket.all = [];
  document.body.innerHTML = '';
});

describe('the clock and timer overlay, in Classic', () => {
  it('formats lengths as minutes and seconds, with hours when there are some', () => {
    expect(duration(0)).toBe('0:00');
    expect(duration(65_000)).toBe('1:05');
    expect(duration(3_723_000)).toBe('1:02:03');
    expect(duration(-5)).toBe('0:00');
  });

  it('shows the time on a 12- or 24-hour clock, with seconds and the date if chosen', () => {
    const { ws, c, stage, digits } = page(hello({ timer_mode: 'clock', timer_label: 'Local time' }));
    expect(stage.querySelector('.sg-timer-label')?.textContent).toBe('Local time');
    expect(digits()).toMatch(/^6:30\s?PM$/);
    expect((stage.querySelector('.sg-timer-date') as HTMLElement).hidden).toBe(true);
    ws.fire('message', hello({ timer_mode: 'clock', clock_24h: true, clock_seconds: true, clock_date: 'long' }));
    c.advance(5_000);
    expect(digits()).toBe('18:30:05');
    expect(stage.querySelector('.sg-timer-date')?.textContent).toContain('2026');
    expect((stage.querySelector('.sg-timer-label') as HTMLElement).hidden).toBe(true);
    expect(stage.querySelectorAll('.sg-timer')).toHaveLength(1); // a new look redraws the same box
  });

  it('counts a countdown down from the engine’s state, and shows its end text at zero', () => {
    const { ws, c, digits } = page(hello({ timer_mode: 'countdown', timer_length: 300, timer_end_text: 'Starting now!' }));
    expect(digits()).toBe('5:00'); // not started: its full length
    // Started 10 seconds ago on the engine's clock, which is 2 seconds ahead of this page's.
    const now = c.now();
    ws.fire('message', { type: 'timer', body: { running: true, base_ms: 300_000, since_ms: now - 8_000, now_ms: now + 2_000 } });
    expect(digits()).toBe('4:50');
    c.advance(30_000);
    expect(digits()).toBe('4:20');
    c.advance(290_000);
    expect(digits()).toBe('Starting now!');
    // Paused with time left: it stays.
    ws.fire('message', { type: 'timer', body: { running: false, base_ms: 61_000, since_ms: c.now(), now_ms: c.now() } });
    c.advance(10_000);
    expect(digits()).toBe('1:01');
    // Reset: its length again.
    ws.fire('message', { type: 'timer', body: null });
    expect(digits()).toBe('5:00');
  });

  it('counts a stopwatch up, with hours past an hour', () => {
    const { ws, c, digits } = page(hello({ timer_mode: 'stopwatch' }));
    expect(digits()).toBe('0:00');
    ws.fire('message', { type: 'timer', body: { running: true, base_ms: 3_590_000, since_ms: c.now(), now_ms: c.now() } });
    c.advance(15_000);
    expect(digits()).toBe('1:00:05');
  });

  it('says it loaded once per load, never on a reconnect', () => {
    const { ws } = page(hello({ timer_mode: 'countdown', timer_auto_start: true }));
    expect(ws.sent.filter((m) => m.type === 'loaded')).toHaveLength(1);
    ws.fire('message', hello({ timer_mode: 'countdown', timer_auto_start: true })); // a new look, or a reconnect's hello
    expect(ws.sent.filter((m) => m.type === 'loaded')).toHaveLength(1);
  });

  it('never runs markup in its label or end text', () => {
    const { stage } = page(hello({ timer_mode: 'clock', timer_label: '<img src=x onerror=alert(1)>' }));
    expect(stage.querySelector('img')).toBeNull();
    expect(stage.querySelector('.sg-timer-label')?.textContent).toBe('<img src=x onerror=alert(1)>');
  });
});

describe('the clock and timer overlay with a look', () => {
  const url = 'http://127.0.0.1:50123/t/flip-clock/1.0.0/abc/index.html';
  const theme = { id: 'flip-clock', name: 'Flip clock', url, settings: { accent: '#ff9900' }, add_on: true };

  function themed() {
    const p = page(hello({ timer_mode: 'countdown', timer_length: 120, timer_label: 'Starting soon' }, theme));
    const frame = document.querySelector('iframe')!;
    const posted: { type: string; payload: Record<string, unknown> }[] = [];
    (frame.contentWindow as unknown as { postMessage: unknown }).postMessage = (m: (typeof posted)[0]) => posted.push(m);
    const fromLook = (type: string, payload: unknown = {}, source: unknown = frame.contentWindow) =>
      window.dispatchEvent(new MessageEvent('message', { source: source as Window, data: { protocolVersion: 1, type, payload } }));
    const classic = () => p.stage.querySelector('.sg-timer') as HTMLElement;
    return { ...p, frame, posted, fromLook, classic };
  }

  it('frames the look sandboxed; nothing shows while it loads, so Classic never comes in first', () => {
    const { frame, classic, fromLook, posted } = themed();
    expect(frame.getAttribute('sandbox')).toBe('allow-scripts');
    expect(frame.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(frame.classList.contains('sg-timer-frame')).toBe(true); // it comes in as the overlay's animation says
    expect(frame.classList.contains('on')).toBe(false);
    expect(classic().hidden).toBe(true);
    fromLook('theme.ready', { apiVersion: '1', obsApi: false });
    expect(frame.classList.contains('on')).toBe(true);
    expect(classic().hidden).toBe(true);
    expect(posted.at(-1)).toMatchObject({
      type: 'timer.snapshot',
      payload: { settings: { accent: '#ff9900' }, timer: { mode: 'countdown', position: 'top', label: 'Starting soon', lengthMs: 120_000, valueMs: 120_000, running: false, direction: 0 } },
    });
  });

  it('shows Classic if the look isn’t ready in 5 seconds', () => {
    const { c, classic, frame, fromLook } = themed();
    c.advance(4_900);
    expect(classic().hidden).toBe(true);
    c.advance(200);
    expect(classic().hidden).toBe(false);
    expect(document.getElementById('status')!.textContent).toContain("Flip clock didn't start");
    fromLook('theme.ready', {}); // late, but ready: the look shows instead
    expect(frame.classList.contains('on')).toBe(true);
    expect(classic().hidden).toBe(true);
  });

  it('tells the look where the overlay is placed', () => {
    const { ws, fromLook, posted } = themed();
    fromLook('theme.ready', {});
    ws.fire('message', hello({ timer_mode: 'countdown', timer_length: 120, position: 'bottom-right' }, theme));
    expect(posted.at(-1)).toMatchObject({ type: 'timer.updated', payload: { timer: { position: 'bottom-right' } } });
  });

  it('tells the look each change, with where it is and when', () => {
    const { ws, c, fromLook, posted } = themed();
    fromLook('theme.ready', {});
    ws.fire('message', { type: 'timer', body: { running: true, base_ms: 120_000, since_ms: c.now(), now_ms: c.now() } });
    expect(posted.at(-1)).toMatchObject({ type: 'timer.updated', payload: { timer: { valueMs: 120_000, running: true, direction: -1, atMs: c.now() } } });
  });

  it('falls back to Classic when the look fails, and ignores anything else it says or anyone else', () => {
    const { frame, classic, fromLook, posted, status } = { ...themed(), status: document.getElementById('status')! };
    fromLook('theme.ready', {});
    fromLook('timer.start', { anything: true }); // not the look's to say
    fromLook('theme.error', {}, window); // not from the frame
    expect(frame.classList.contains('on')).toBe(true);
    fromLook('theme.error', { message: 'no fonts' });
    expect(frame.classList.contains('on')).toBe(false);
    expect(classic().hidden).toBe(false);
    expect(status.textContent).toContain('shows in Classic');
    const n = posted.length;
    fromLook('theme.ready', {});
    expect(posted.length).toBe(n); // failed: not shown again
  });

  it('never frames a look from anywhere but the theme server', () => {
    page(hello({ timer_mode: 'clock' }, { ...theme, url: 'https://evil.example/t/x/index.html' }));
    expect(document.querySelector('iframe')).toBeNull();
    expect(document.querySelector('.sg-timer')).not.toBeNull();
  });
});

describe('the Flip clock example look', () => {
  type Look = { reading: (t: unknown, now: number) => { text: string; note: string }; flipClock: (win: unknown, opts?: unknown) => void };
  let mod: Look;
  beforeAll(async () => {
    (window as unknown as Record<string, boolean>).__streamgremlinThemeTest = true;
    const p = new URL('../../../../../examples/themes/flip-clock/clock.js', import.meta.url).pathname;
    mod = (await import(/* @vite-ignore */ p)) as Look;
  });

  it('reads a clock, a countdown and a stopwatch from what it’s told', () => {
    const at = new Date(2026, 10, 13, 18, 5, 9).getTime();
    expect(mod.reading({ mode: 'clock', clock: { hour24: false, seconds: false } }, at).text).toBe('6:05 PM');
    expect(mod.reading({ mode: 'clock', clock: { hour24: true, seconds: true, date: 'short' } }, at)).toMatchObject({ text: '18:05:09' });
    expect(mod.reading({ mode: 'clock', clock: { date: 'long' } }, at).note).toContain('2026');
    const down = { mode: 'countdown', valueMs: 300_000, atMs: at, direction: -1, endText: 'Starting now!' };
    expect(mod.reading(down, at + 61_000).text).toBe('3:59');
    expect(mod.reading(down, at + 400_000)).toEqual({ text: '', note: 'Starting now!' });
    expect(mod.reading({ ...down, direction: 0 }, at + 400_000).text).toBe('5:00'); // paused
    expect(mod.reading({ mode: 'stopwatch', valueMs: 3_595_000, atMs: at, direction: 1 }, at + 10_000).text).toBe('1:00:05');
  });

  it('says it’s ready, draws a card per digit, and flips the ones that change', () => {
    document.body.innerHTML = '<main id="stage" hidden><p id="label"></p><div id="cards"></div><p id="note"></p></main>';
    const sent: { type: string }[] = [];
    let now = new Date(2026, 10, 13, 18, 30, 0).getTime();
    const timers: (() => void)[] = [];
    const parent = { postMessage: (m: { type: string }) => sent.push(m) };
    const listeners: ((ev: MessageEvent) => void)[] = [];
    const win = { document, parent, addEventListener: (_: string, fn: (ev: MessageEvent) => void) => listeners.push(fn) };
    mod.flipClock(win, { setTimeout: (fn: () => void) => timers.push(fn), now: () => now });
    expect(sent).toEqual([{ protocolVersion: 1, type: 'theme.ready', payload: { apiVersion: '1', obsApi: false } }]);
    const tell = (type: string, payload: unknown, source: unknown = parent) =>
      listeners.forEach((fn) => fn({ source, data: { protocolVersion: 1, type, payload } } as unknown as MessageEvent));
    tell('timer.snapshot', {
      settings: { cards: 'Cream', accent: '#00ff00', show_label: true, size: 120 }, reducedMotion: false,
      timer: { mode: 'countdown', label: 'Starting soon', valueMs: 65_000, atMs: now, direction: -1, endText: '' },
    });
    expect(document.getElementById('stage')!.hidden).toBe(false);
    expect(document.getElementById('label')!.textContent).toBe('Starting soon');
    const cards = () => Array.from(document.querySelectorAll('#cards > span')).map((e) => e.textContent).join('');
    expect(cards()).toBe('1:05');
    expect(document.documentElement.style.getPropertyValue('--size')).toBe('1.2');
    now += 1_000;
    timers.shift()!();
    expect(cards()).toBe('1:04');
    expect(document.querySelectorAll('.card.flip')).toHaveLength(1); // only the digit that changed
    // Someone else's message is ignored.
    tell('timer.updated', { timer: { mode: 'stopwatch', valueMs: 0, atMs: now, direction: 0 } }, window);
    expect(cards()).toBe('1:04');
    tell('theme.settings.updated', { settings: { show_label: false } });
    expect(document.getElementById('label')!.hidden).toBe(true);
  });
});

describe('the built-in timer looks', () => {
  type Mod = { reading: (t: unknown, now: number) => { text: string; note: string; done: boolean }; timerTheme: (win: unknown, opts?: unknown) => void };
  let mod: Mod;
  beforeAll(async () => {
    (window as unknown as Record<string, boolean>).__streamgremlinThemeTest = true;
    mod = (await load('themes/shared/timertheme.js')) as unknown as Mod;
  });

  it('read a clock, a countdown (with its end text) and a stopwatch', () => {
    const at = new Date(2026, 10, 13, 18, 5, 9).getTime();
    expect(mod.reading({ mode: 'clock', clock: { hour24: true, seconds: true } }, at).text).toBe('18:05:09');
    const down = { mode: 'countdown', valueMs: 90_000, atMs: at, direction: -1, endText: 'Live!' };
    expect(mod.reading(down, at + 30_500).text).toBe('1:00');
    expect(mod.reading(down, at + 100_000)).toEqual({ text: 'Live!', note: '', done: true });
    expect(mod.reading({ mode: 'stopwatch', valueMs: 0, atMs: at, direction: 1 }, at + 75_000).text).toBe('1:15');
  });

  it('show the label in the title plate and the time on the board, as told, and say they’re ready', () => {
    document.body.innerHTML = '<main id="app"></main>';
    let now = new Date(2026, 10, 13, 18, 30, 0).getTime();
    const ticks: (() => void)[] = [];
    const sent: { type: string }[] = [];
    const parent = { postMessage: (m: { type: string }) => sent.push(m) };
    const listeners: ((ev: MessageEvent) => void)[] = [];
    const win = { document, parent, addEventListener: (_: string, fn: (ev: MessageEvent) => void) => listeners.push(fn) };
    mod.timerTheme(win, { setTimeout: (fn: () => void) => ticks.push(fn), now: () => now });
    expect(sent.at(-1)).toMatchObject({ type: 'theme.ready' });
    const app = document.getElementById('app')!;
    expect(app.hidden).toBe(true); // nothing until told
    const tell = (type: string, payload: unknown, source: unknown = parent) =>
      listeners.forEach((fn) => fn({ source, data: { protocolVersion: 1, type, payload } } as unknown as MessageEvent));
    tell('timer.snapshot', {
      settings: { show_label: true, accent: '#ff00aa' },
      timer: { mode: 'countdown', label: '<b>Starting soon</b>', valueMs: 300_000, atMs: now, direction: -1, endText: '' },
    });
    expect(app.hidden).toBe(false);
    expect(app.querySelector('.title')!.textContent).toBe('<b>Starting soon</b>');
    expect(app.querySelector('b')).toBeNull();
    expect(app.querySelector('.digits')!.textContent).toBe('5:00');
    expect(app.querySelector('.plate')!.classList.contains('running')).toBe(true);
    // No position given: the middle, as before.
    expect([app.style.justifyContent, app.style.alignItems]).toEqual(['center', 'center']);
    tell('timer.updated', { timer: { mode: 'countdown', position: 'top-right', label: 'x', valueMs: 300_000, atMs: now, direction: -1 } });
    expect([app.style.justifyContent, app.style.alignItems]).toEqual(['flex-start', 'flex-end']);
    expect(document.documentElement.style.getPropertyValue('--accent')).toBe('#ff00aa');
    now += 61_000;
    ticks.shift()!();
    expect(app.querySelector('.digits')!.textContent).toBe('3:59');
    tell('theme.settings.updated', { settings: { show_label: false, accent: 'red' } });
    expect((app.querySelector('.title') as HTMLElement).hidden).toBe(true);
    expect(document.documentElement.style.getPropertyValue('--accent')).toBe('#ff00aa'); // not a colour: kept
    tell('timer.updated', { timer: { mode: 'clock', label: '', clock: {} } }, window); // not from the overlay page
    expect(app.querySelector('.digits')!.textContent).toBe('3:59');
  });
});
