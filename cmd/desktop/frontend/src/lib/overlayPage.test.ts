// The overlay page OBS loads (internal/overlays/page/overlay.js): plain
// JavaScript served by the engine, tested here with a fake connection.
import { afterEach, beforeAll, describe, expect, it } from 'vitest';

type Start = (win: unknown, opts?: unknown) => { stop: () => void };
let start: Start;

beforeAll(async () => {
  (window as unknown as { __streamgremlinOverlayTest: boolean }).__streamgremlinOverlayTest = true;
  const path = new URL('../../../../../internal/overlays/page/overlay.js', import.meta.url).pathname;
  start = ((await import(/* @vite-ignore */ path)) as { start: Start }).start;
});

class FakeSocket {
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: Record<string, string>[] = [];
  listeners: Record<string, ((ev: { data?: string }) => void)[]> = {};
  constructor(
    readonly url: string,
    readonly protocol: string,
  ) {
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
    if (kind === 'close') this.readyState = 3;
    for (const fn of this.listeners[kind] ?? []) fn({ data: data === undefined ? undefined : JSON.stringify(data) });
  }
}

// A clock the test moves by hand.
function clock() {
  let now = 0;
  let timers: { at: number; fn: () => void }[] = [];
  return {
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

function page(search = '') {
  document.body.innerHTML = '<div id="status" hidden></div><div id="stage" class="pos-top anim-slide"></div>';
  const c = clock();
  const win = {
    document,
    location: { search, pathname: '/o/tok123/', protocol: 'http:', host: '127.0.0.1:7431' },
  };
  const ov = start(win, { WebSocket: FakeSocket, setTimeout: c.setTimeout });
  const ws = FakeSocket.all.at(-1)!;
  ws.fire('open');
  return { ov, ws, c, stage: document.getElementById('stage')!, status: document.getElementById('status')! };
}

const alert = (id: string, extra: Record<string, unknown> = {}) => ({
  type: 'alert',
  body: { alert: { id, text: 'Fan followed', seconds: 3, ...extra }, ms_left: 3000 },
});

afterEach(() => {
  FakeSocket.all = [];
  document.body.innerHTML = '';
  document.documentElement.removeAttribute('style');
});

describe('overlay page', () => {
  it('connects to its own link and applies the look', () => {
    const { ws, stage } = page();
    expect(ws.url).toBe('ws://127.0.0.1:7431/o/tok123/ws');
    expect(ws.protocol).toBe('streamgremlin.overlay.v1');
    ws.fire('message', {
      type: 'hello',
      body: { overlay: { look: { position: 'bottom-right', animation: 'pop', text_color: '#112233', background: '#ffffff', opacity: 50 } } },
    });
    expect(stage.className).toBe('pos-bottom-right anim-pop');
    expect(document.documentElement.style.getPropertyValue('--sg-text')).toBe('#112233');
    expect(document.documentElement.style.getPropertyValue('--sg-bg')).toBe('rgba(255, 255, 255, 0.5)');
    // Nonsense in a look changes nothing it shouldn't.
    ws.fire('message', { type: 'hello', body: { overlay: { look: { position: 'x"><b>', text_color: 'red;}' } } } });
    expect(stage.className).toBe('pos-top anim-slide');
    expect(document.documentElement.style.getPropertyValue('--sg-text')).toBe('#112233');
  });

  it('shows text as text, says it started and when it left, and plays alerts in turn', () => {
    const { ws, stage, c } = page();
    ws.fire('message', alert('a1', { title: 'New follower', text: '<img src=x onerror=alert(1)>Fan' }));
    ws.fire('message', alert('a2'));
    const box = stage.querySelector('.sg-alert')!;
    expect(stage.querySelectorAll('.sg-alert').length).toBe(1);
    expect(box.querySelector('.sg-text')!.textContent).toBe('<img src=x onerror=alert(1)>Fan');
    expect(box.querySelector('img')).toBeNull();
    expect(box.querySelector('.sg-title')!.textContent).toBe('New follower');
    expect(box.classList.contains('in')).toBe(true);
    expect(ws.sent).toEqual([{ type: 'ack', id: 'a1', status: 'started' }]);
    c.advance(3000);
    expect(ws.sent.slice(1)).toEqual([
      { type: 'ack', id: 'a1', status: 'done' },
      { type: 'ack', id: 'a2', status: 'started' },
    ]);
    expect(stage.querySelectorAll('.sg-alert').length).toBe(1);
    // The same alert again (a reconnect resends it) isn't played twice.
    ws.fire('message', alert('a1'));
    c.advance(3000);
    expect(ws.sent.filter((m) => m.id === 'a1' && m.status === 'started').length).toBe(1);
  });

  it("shows only Twitch's profile pictures", () => {
    const { ws, stage, c } = page();
    ws.fire('message', alert('p1', { picture: 'https://static-cdn.jtvnw.net/jtv_user_pictures/fan.png' }));
    expect(stage.querySelector('img')?.getAttribute('src')).toBe('https://static-cdn.jtvnw.net/jtv_user_pictures/fan.png');
    c.advance(3000);
    ws.fire('message', alert('p2', { picture: 'https://evil.example/x.png' }));
    expect(stage.querySelector('img')).toBeNull();
  });

  it('marks previews, shows their status, and reconnects after a drop', () => {
    const { ws, status, c } = page('?preview=1');
    expect(ws.url).toBe('ws://127.0.0.1:7431/o/tok123/ws?preview=1');
    ws.fire('close');
    expect(status.hidden).toBe(false);
    expect(status.textContent).toContain('Not connected');
    c.advance(1000);
    expect(FakeSocket.all.length).toBe(2);
  });

  it('never shows a status in OBS unless asked', () => {
    const { ws, status } = page();
    ws.fire('close');
    expect(status.hidden).toBe(true);
  });
});
