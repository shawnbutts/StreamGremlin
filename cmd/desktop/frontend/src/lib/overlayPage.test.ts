// The overlay page OBS loads (internal/overlays/page/overlay.js): plain
// JavaScript served by the engine, tested here with a fake connection.
import { afterEach, beforeAll, describe, expect, it } from 'vitest';

type Start = (win: unknown, opts?: unknown) => { stop: () => void };
let start: Start;
let lookStyle: (look: Record<string, unknown>) => Record<string, string>;

beforeAll(async () => {
  (window as unknown as { __streamgremlinOverlayTest: boolean }).__streamgremlinOverlayTest = true;
  const path = new URL('../../../../../internal/overlays/page/overlay.js', import.meta.url).pathname;
  const mod = (await import(/* @vite-ignore */ path)) as { start: Start; lookStyle: typeof lookStyle };
  start = mod.start;
  lookStyle = mod.lookStyle;
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

// A stand-in for the page's audio: records what it was asked to play.
class FakeAudio {
  static all: FakeAudio[] = [];
  static refuse = '';
  volume = 1;
  constructor(readonly src: string) {
    FakeAudio.all.push(this);
  }
  play() {
    return FakeAudio.refuse ? Promise.reject(Object.assign(new Error('no'), { name: FakeAudio.refuse })) : Promise.resolve();
  }
}

function page(search = '', extra: Record<string, unknown> = {}) {
  document.body.innerHTML = '<div id="status" hidden></div><div id="stage" class="pos-top anim-slide"></div>';
  const c = clock();
  const win = {
    document,
    location: { search, pathname: '/o/tok123/', protocol: 'http:', host: '127.0.0.1:7431' },
  };
  const ov = start(win, { WebSocket: FakeSocket, setTimeout: c.setTimeout, Audio: (src: string) => new FakeAudio(src), ...extra });
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
  FakeAudio.all = [];
  FakeAudio.refuse = '';
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

  it('draws gradients, outlines, shadows, fonts and sizes from checked values only', () => {
    const st = lookStyle({
      text_color: '#ffffff', title_color: '#ffcc00', background: '#6a00ff', background2: '#ff0080', gradient_angle: 90, opacity: 80,
      radius: 24, border_width: 3, border_color: '#ffffff', shadow: 0, shadow_color: '#000000',
      font: 'custom', font_name: 'Comic Neue', text_size: 150, text_outline: 2, outline_color: '#000000', text_shadow: 50,
    });
    expect(st['--sg-bg']).toBe('linear-gradient(90deg, rgba(106, 0, 255, 0.8), rgba(255, 0, 128, 0.8))');
    expect(st['--sg-title']).toBe('#ffcc00');
    expect(st['--sg-radius']).toBe('24px');
    expect(st['--sg-border']).toBe('3px solid #ffffff');
    expect(st['--sg-shadow']).toBe('none');
    expect(st['--sg-font']).toMatch(/^"Comic Neue", system-ui/);
    expect(st['--sg-scale']).toBe('1.5');
    expect(st['--sg-text-shadow']).toMatch(/3\.0px 3\.0px 5\.0px rgba\(0, 0, 0, 0\.65\)$/); // the drop shadow, last
    expect(st['--sg-text-shadow']).toContain('2.00px 0.00px 0 #000000');
    // A solid box, no outline, no text shadow.
    const plain = lookStyle({ background: '#081923', opacity: 90, shadow: 35, text_outline: 0, text_shadow: 0, font: 'serif' });
    expect(plain['--sg-bg']).toBe('rgba(8, 25, 35, 0.9)');
    expect(plain['--sg-text-shadow']).toBe('none');
    expect(plain['--sg-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.35)');
    expect(plain['--sg-font']).toMatch(/^Georgia/);
    // Anything that could end the CSS value falls back, never passes through.
    const bad = lookStyle({
      font: 'custom', font_name: 'Arial"; } body { color: red', background: '#000000', background2: 'red); x: (',
      border_width: 99, border_color: 'red', text_size: 9999, radius: -5, text_color: 'url(x)',
    });
    expect(bad['--sg-font']).toMatch(/^system-ui/);
    expect(bad['--sg-bg']).toBe('rgba(0, 0, 0, 1)');
    expect(bad['--sg-border']).toBe('none');
    expect(bad['--sg-scale']).toBe('2');
    expect(bad['--sg-radius']).toBe('0px');
    expect(bad['--sg-text']).toBeUndefined();
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

  it('unfolds: the picture (or an initial) first, the words beside it, mirrored on the right', () => {
    const { ws, stage, c } = page();
    ws.fire('message', { type: 'hello', body: { overlay: { look: { position: 'bottom-left', animation: 'unfold' } } } });
    expect(stage.className).toBe('pos-bottom-left anim-unfold');
    ws.fire('message', alert('u1', { picture: 'https://static-cdn.jtvnw.net/fan.png', name: 'Fan' }));
    const box = stage.querySelector('.sg-alert')!;
    expect(box.firstElementChild?.className).toBe('sg-picture');
    expect(box.lastElementChild?.className).toBe('sg-words');
    // Leaving takes longer: the words fold away, then the picture.
    expect(lookStyle({ pace: 200 })['--sg-pace']).toBe('2');
    expect(lookStyle({ pace: 999 })['--sg-pace']).toBe('3');
    c.advance(3000 - 750);
    expect(box.classList.contains('out')).toBe(true);
    expect(ws.sent.some((m) => m.status === 'done')).toBe(false);
    c.advance(750);
    expect(ws.sent.some((m) => m.id === 'u1' && m.status === 'done')).toBe(true);
    // No picture: the viewer's initial keeps the shape; no viewer, a star.
    ws.fire('message', alert('u2', { name: 'émile' }));
    expect(stage.querySelector('.sg-avatar')?.textContent).toBe('É');
    c.advance(3000);
    ws.fire('message', alert('u3', {}));
    expect(stage.querySelector('.sg-avatar')?.textContent).toBe('★');
    c.advance(3000);
    // A picture that won't load becomes the initial.
    ws.fire('message', alert('u4', { picture: 'https://static-cdn.jtvnw.net/gone.png', name: 'Pal' }));
    stage.querySelector('img')!.dispatchEvent(new Event('error'));
    expect(stage.querySelector('img')).toBeNull();
    expect(stage.querySelector('.sg-avatar')?.textContent).toBe('P');
    c.advance(3000);
    ws.fire('message', { type: 'hello', body: { overlay: { look: { position: 'top-right', animation: 'unfold' } } } });
    expect(stage.className).toBe('pos-top-right anim-unfold side-right');
    // A slower pace waits longer to leave: 750 ms × 2.
    ws.fire('message', { type: 'hello', body: { overlay: { look: { position: 'top', animation: 'unfold', pace: 200 } } } });
    ws.fire('message', alert('u6', { name: 'Slow' }));
    c.advance(3000 - 1500);
    expect(ws.sent.some((m) => m.id === 'u6' && m.status === 'done')).toBe(false);
    c.advance(1500);
    expect(ws.sent.some((m) => m.id === 'u6' && m.status === 'done')).toBe(true);
    // Other animations never add an initial.
    ws.fire('message', { type: 'hello', body: { overlay: { look: { position: 'top', animation: 'slide' } } } });
    ws.fire('message', alert('u5', { name: 'Fan' }));
    expect(stage.querySelector('.sg-avatar')).toBeNull();
  });

  it('reloads itself when the engine was updated, and only then', () => {
    document.body.innerHTML = '<div id="status" hidden></div><div id="stage" class="pos-top anim-slide"></div>';
    let reloads = 0;
    const c = clock();
    start(
      { document, location: { search: '', pathname: '/o/tok123/', protocol: 'http:', host: '127.0.0.1:7431' } },
      { WebSocket: FakeSocket, setTimeout: c.setTimeout, Audio: (src: string) => new FakeAudio(src), reload: () => reloads++ },
    );
    const ws = FakeSocket.all.at(-1)!;
    ws.fire('open');
    ws.fire('message', { type: 'hello', body: { page_version: 'v1', overlay: { look: {} } } });
    ws.fire('message', { type: 'hello', body: { page_version: 'v1', overlay: { look: { position: 'bottom' } } } }); // a new look
    expect(reloads).toBe(0);
    // The engine restarted on an update: the page reconnects and is told.
    ws.fire('close');
    c.advance(1000);
    const again = FakeSocket.all.at(-1)!;
    again.fire('open');
    again.fire('message', { type: 'hello', body: { page_version: 'v2', overlay: { look: {} } } });
    expect(reloads).toBe(1);
  });

  it('plays the alert sound in the page, says whether it started, and never in a preview', async () => {
    const { ws, c } = page();
    ws.fire('message', alert('s1', { sound: { name: 'chime', volume: 70 } }));
    expect(FakeAudio.all.length).toBe(1);
    expect(FakeAudio.all[0].src).toBe('/o/tok123/sound/chime');
    expect(FakeAudio.all[0].volume).toBe(0.7);
    await Promise.resolve();
    await Promise.resolve();
    expect(ws.sent).toContainEqual({ type: 'ack', id: 's1', status: 'sound_started' });
    c.advance(3000);
    FakeAudio.refuse = 'NotAllowedError';
    ws.fire('message', alert('s2', { sound: { name: 'chime', volume: 70 } }));
    await Promise.resolve();
    await Promise.resolve();
    expect(ws.sent).toContainEqual({ type: 'ack', id: 's2', status: 'sound_error', detail: 'NotAllowedError' });
    c.advance(3000);
    // A name that isn't a sound's is never fetched.
    ws.fire('message', alert('s3', { sound: { name: '../secrets', volume: 70 } }));
    expect(FakeAudio.all.length).toBe(2);
    c.advance(3000);
    const pv = page('?preview=1');
    pv.ws.fire('message', alert('s4', { sound: { name: 'chime', volume: 70 } }));
    expect(FakeAudio.all.length).toBe(2);
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

describe('the giveaway overlay', () => {
  const hello = { type: 'hello', body: { overlay: { name: 'giveaway', kind: 'giveaway', look: {} }, page_version: 'v1' } };
  const state = (extra: Record<string, unknown> = {}) => ({
    type: 'giveaway',
    body: {
      id: 'g', title: 'a mug', state: 'open', how_to_enter: 'Type !join in chat to enter', chances: 'every entry has the same chance',
      entries: 3, winners: [], ...extra,
    },
  });
  const draw = (id: string, name: string, roll: string[]) => ({ type: 'draw', body: { id, giveaway: 'g', title: 'a mug', n: 1, name, roll } });

  it('shows the giveaway, and rolls to the winner it was sent, landing as chat names them', () => {
    let now = Date.parse('2026-11-10T12:00:00Z');
    const { ws, c, stage } = page('', { now: () => now });
    ws.fire('message', hello);
    ws.fire('message', state({ closes_at: '2026-11-10T12:02:05Z', who: 'followers' }));
    expect(stage.textContent).toContain('🎉 a mug');
    expect(stage.textContent).toContain('Type !join in chat to enter');
    expect(stage.textContent).toContain('3 entries · closes in 2:05');
    expect(stage.textContent).toContain('Open to followers');
    ws.fire('message', draw('d1', 'Ann', ['Bob', 'Cat', 'Ann']));
    expect(stage.textContent).toContain('Drawing a mug…');
    c.advance(3400);
    expect(stage.textContent).not.toContain('Winner of a mug');
    ws.fire('message', { type: 'picture', body: { id: 'd1', picture: 'https://evil.example/x.png' } });
    c.advance(100); // 3.5 seconds: the roll lands on Ann
    expect(stage.querySelector('.sg-landed')?.textContent).toBe('Ann');
    expect(stage.textContent).not.toContain('Winner of a mug');
    c.advance(500); // a beat later, the winner shows (as chat names them)
    expect(stage.textContent).toContain('Winner of a mug');
    expect(stage.textContent).toContain('🎉 Ann');
    expect(stage.querySelector('img')).toBeNull(); // only Twitch pictures
    expect(stage.querySelector('.sg-avatar')?.textContent).toBe('A');
    now += 10000;
    c.advance(5000);
    expect(stage.textContent).toContain('🎉 a mug');
    expect(stage.textContent).not.toContain('Winner of');
  });

  it('plays winners one after another, with their pictures, and lands on the winner even if the roll disagrees', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello);
    ws.fire('message', state());
    ws.fire('message', draw('d1', 'Ann', ['Ann', 'Bob']));
    ws.fire('message', draw('d2', 'Bob', ['Ann', 'Zed'])); // the roll's last isn't Bob: Bob still wins
    ws.fire('message', { type: 'picture', body: { id: 'd2', picture: 'https://static-cdn.jtvnw.net/bob.png' } });
    c.advance(4000);
    expect(stage.textContent).toContain('🎉 Ann');
    c.advance(5000 + 4000);
    expect(stage.textContent).toContain('🎉 Bob');
    expect(stage.textContent).not.toContain('Zed');
    expect(stage.querySelector('img')?.getAttribute('src')).toBe('https://static-cdn.jtvnw.net/bob.png');
  });

  it('shows the winner at once with reduced motion, and names stay text', () => {
    const { ws, stage } = page('', { reducedMotion: () => true });
    ws.fire('message', hello);
    ws.fire('message', state({ title: '<b>bold</b>' }));
    ws.fire('message', draw('d1', '<img src=x>', ['<img src=x>']));
    expect(stage.textContent).toContain('🎉 <img src=x>');
    expect(stage.querySelector('b')).toBeNull();
    expect(stage.querySelectorAll('img').length).toBe(0);
  });

  it('says who won when it ends, then goes; a cancelled giveaway goes at once; alert overlays ignore it', () => {
    let now = Date.parse('2026-11-10T12:00:00Z');
    const { ws, c, stage } = page('', { now: () => now });
    ws.fire('message', hello);
    ws.fire('message', state({ state: 'ended', ended_at: '2026-11-10T12:00:00Z', winners: [{ draw: 1, name: 'Ann', outcome: 'claimed' }] }));
    expect(stage.textContent).toContain('Congratulations, Ann!');
    now += 21000;
    c.advance(1000);
    expect(stage.textContent).toBe('');
    ws.fire('message', state({ state: 'cancelled' }));
    expect(stage.textContent).toBe('');

    const other = page();
    other.ws.fire('message', { type: 'hello', body: { overlay: { name: 'alerts', kind: 'alert', look: {} }, page_version: 'v1' } });
    other.ws.fire('message', state());
    other.ws.fire('message', draw('d9', 'Ann', ['Ann']));
    expect(other.stage.textContent).toBe('');
  });
});

describe('the wheel overlay', () => {
  const hello = { type: 'hello', body: { overlay: { name: 'wheel', kind: 'wheel', look: {} }, page_version: 'v1' } };
  const choices = [
    { label: 'Mario', weight: 2 },
    { label: 'Zelda', weight: 1 },
    { label: 'Tetris', weight: 1, color: '#ff8800' },
  ];
  const spin = (id: string, winner: number, draw: number, extra: Record<string, unknown> = {}) => ({
    type: 'spin',
    body: { id, title: 'Next game', choices, winner, draw, seconds: 6, ...extra },
  });

  it('draws wedges sized to their weights, spins to the drawn number, then shows the result and goes', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello);
    ws.fire('message', spin('s1', 0, 1));
    const paths = stage.querySelectorAll('path');
    expect(paths.length).toBe(3);
    expect(stage.textContent).toContain('Next game');
    // Mario holds 2 of 4 slots (180°); number 1 is the middle of the second slot: 135° from the top.
    const rotor = stage.querySelector('.sg-wheel-rotor') as SVGGElement;
    expect(rotor.style.transform).toBe(`rotate(${5 * 360 + 360 - 135}deg)`);
    expect(stage.textContent).not.toContain('🎉');
    c.advance(6000);
    expect(stage.textContent).not.toContain('🎉'); // stopped; the result a beat later
    c.advance(500);
    expect(stage.textContent).toContain('🎉 Mario');
    c.advance(4000);
    expect(stage.textContent).toBe('');
  });

  it('plays spins in turn, never shows a spin that doesn’t add up, and keeps labels as text', () => {
    const { ws, c, stage } = page('', { reducedMotion: () => true });
    ws.fire('message', hello);
    ws.fire('message', spin('bad', 1, 0)); // number 0 is Mario's, not Zelda's
    expect(stage.textContent).toBe('');
    ws.fire('message', spin('s2', 2, 3, { title: '<b>x</b>' }));
    ws.fire('message', spin('s2', 2, 3)); // a repeat is ignored
    ws.fire('message', spin('s3', 1, 2));
    expect(stage.textContent).toContain('🎉 Tetris');
    expect(stage.querySelector('b')).toBeNull();
    c.advance(4000);
    expect(stage.textContent).toContain('🎉 Zelda');
    c.advance(4000);
    expect(stage.textContent).toBe('');
  });
});

describe('giveaway and wheel overlay sounds', () => {
  const look = { start_sound: 'drumroll', start_volume: 40, end_sound: 'clunk', end_volume: 90, cheer_sound: 'cheer', cheer_volume: 60 };
  const hello = (kind: string) => ({ type: 'hello', body: { overlay: { name: kind, kind, look }, page_version: 'v1' } });

  it('plays the start sound as a roll starts, the end sound as it lands and the cheer as the winner shows, through the overlay’s link', () => {
    FakeAudio.all = [];
    const { ws, c } = page();
    ws.fire('message', hello('giveaway'));
    ws.fire('message', { type: 'giveaway', body: { id: 'g', title: 'a mug', state: 'closed', winners: [] } });
    ws.fire('message', { type: 'draw', body: { id: 'd1', title: 'a mug', name: 'Ann', roll: ['Bob', 'Ann'] } });
    expect(FakeAudio.all.map((a) => [a.src, a.volume])).toEqual([['/o/tok123/sound/drumroll', 0.4]]);
    c.advance(3500);
    expect(FakeAudio.all.map((a) => a.src)).toEqual(['/o/tok123/sound/drumroll', '/o/tok123/sound/clunk']);
    expect(FakeAudio.all[1].volume).toBe(0.9);
    c.advance(500);
    expect(FakeAudio.all.map((a) => [a.src, a.volume])).toEqual([
      ['/o/tok123/sound/drumroll', 0.4],
      ['/o/tok123/sound/clunk', 0.9],
      ['/o/tok123/sound/cheer', 0.6],
    ]);
  });

  it('does the same for a wheel, and the app’s preview stays silent', () => {
    FakeAudio.all = [];
    const choices = [
      { label: 'A', weight: 1 },
      { label: 'B', weight: 1 },
    ];
    const { ws, c } = page();
    ws.fire('message', hello('wheel'));
    ws.fire('message', { type: 'spin', body: { id: 's1', title: 'Coin', choices, winner: 1, draw: 1, seconds: 2 } });
    expect(FakeAudio.all.map((a) => a.src)).toEqual(['/o/tok123/sound/drumroll']);
    c.advance(2000);
    expect(FakeAudio.all.map((a) => a.src)).toEqual(['/o/tok123/sound/drumroll', '/o/tok123/sound/clunk']);
    c.advance(500);
    expect(FakeAudio.all.map((a) => a.src)).toEqual(['/o/tok123/sound/drumroll', '/o/tok123/sound/clunk', '/o/tok123/sound/cheer']);

    FakeAudio.all = [];
    const p = page('?preview=1');
    p.ws.fire('message', hello('wheel'));
    p.ws.fire('message', { type: 'spin', body: { id: 's2', title: 'Coin', choices, winner: 0, draw: 0, seconds: 2 } });
    p.c.advance(2500);
    expect(FakeAudio.all).toEqual([]);
  });
});

describe('the Now playing overlay', () => {
  const hello = (look: Record<string, unknown> = {}) => ({
    type: 'hello',
    body: { overlay: { name: 'song', kind: 'song', look: { animation: 'slide', pace: 100, ...look } }, page_version: 'v1' },
  });
  const song = (title: string, artist = '', playing = true) => ({ type: 'song', body: { playing, title, artist } });
  const shown = (stage: HTMLElement) => [...stage.querySelectorAll('.sg-song:not(.out)')];

  it('shows the song while it plays, swaps on a change, and goes when nothing plays', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello());
    ws.fire('message', song('Neon Rain', 'Kyle'));
    expect(shown(stage)).toHaveLength(1);
    expect(stage.querySelector('.sg-song-title')?.textContent).toBe('Neon Rain');
    expect(stage.querySelector('.sg-song-artist')?.textContent).toBe('Kyle');
    expect(stage.querySelectorAll('.sg-eq')).toHaveLength(3);
    // The same song again changes nothing.
    ws.fire('message', song('Neon Rain', 'Kyle'));
    expect(stage.querySelectorAll('.sg-song')).toHaveLength(1);
    // A new song: the old one leaves, then the new one comes in. No artist: no line for one.
    ws.fire('message', song('Caffeine Part 2'));
    expect(shown(stage)).toHaveLength(0);
    c.advance(450);
    expect(shown(stage).map((e) => e.querySelector('.sg-song-title')?.textContent)).toEqual(['Caffeine Part 2']);
    expect(stage.querySelector('.sg-song:not(.out) .sg-song-artist')).toBeNull();
    c.advance(1000);
    expect(stage.querySelectorAll('.sg-song')).toHaveLength(1);
    // Nothing playing, or unknown: nothing shown.
    ws.fire('message', { type: 'song', body: { playing: false } });
    c.advance(1000);
    expect(stage.querySelectorAll('.sg-song')).toHaveLength(0);
  });

  it('shows each song for a while when set to, and never runs markup', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello({ song_hides: true, seconds: 5 }));
    ws.fire('message', song('<img src=x onerror=alert(1)>', '<b>Bold</b>'));
    expect(stage.querySelector('img')).toBeNull();
    expect(stage.querySelector('b')).toBeNull();
    expect(stage.querySelector('.sg-song-title')?.textContent).toBe('<img src=x onerror=alert(1)>');
    c.advance(4900);
    expect(shown(stage)).toHaveLength(1);
    c.advance(1000);
    expect(stage.querySelectorAll('.sg-song')).toHaveLength(0);
  });

  it('shows a sample for a moment, then the real song again', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello());
    ws.fire('message', song('Neon Rain', 'Kyle'));
    ws.fire('message', { type: 'song', body: { playing: true, title: 'A sample song', artist: 'A sample artist', sample: true } });
    c.advance(1000);
    expect(shown(stage).map((e) => e.querySelector('.sg-song-title')?.textContent)).toEqual(['A sample song']);
    c.advance(6000);
    expect(shown(stage).map((e) => e.querySelector('.sg-song-title')?.textContent)).toEqual(['Neon Rain']);
    // With nothing playing, a sample goes away again.
    ws.fire('message', { type: 'song', body: { playing: false } });
    ws.fire('message', { type: 'song', body: { playing: true, title: 'A sample song', sample: true } });
    c.advance(7000);
    expect(shown(stage)).toHaveLength(0);
  });

  it('shows the cover through its own link only, and the bars if it will not load', () => {
    const { ws, c, stage } = page();
    ws.fire('message', hello());
    ws.fire('message', { type: 'song', body: { playing: true, title: 'Neon Rain', picture: 'art?v=18e7-4b' } });
    const img = stage.querySelector<HTMLImageElement>('.sg-song-art')!;
    expect(img.getAttribute('src')).toBe('art?v=18e7-4b');
    expect(stage.querySelector('.sg-eq')).toBeNull();
    img.dispatchEvent(new Event('error'));
    expect(stage.querySelectorAll('.sg-eq')).toHaveLength(3);
    // A new cover for the same song is a change; a picture from anywhere else is never loaded.
    ws.fire('message', { type: 'song', body: { playing: true, title: 'Neon Rain', picture: 'https://evil.example/x.jpg' } });
    c.advance(1000);
    expect(stage.querySelector('img')).toBeNull();
    expect(shown(stage)).toHaveLength(1);
  });

  it('other overlays ignore songs', () => {
    const { ws, stage } = page();
    ws.fire('message', { type: 'hello', body: { overlay: { name: 'alerts', kind: 'alert', look: {} }, page_version: 'v1' } });
    ws.fire('message', song('Neon Rain', 'Kyle'));
    expect(stage.textContent).toBe('');
  });
});
