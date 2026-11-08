import { afterEach, describe, expect, it, vi } from 'vitest';
import { mockFetch, settle } from '../test/helpers';
import { MAX_PLAYING, startPlayer, type AudioLike } from './player';

afterEach(() => vi.unstubAllGlobals());

class FakeAudio implements AudioLike {
  static made: FakeAudio[] = [];
  volume = 1;
  paused = true;
  listeners: Record<string, () => void> = {};
  constructor(
    readonly url: string,
    readonly refuse?: string,
  ) {
    FakeAudio.made.push(this);
  }
  async play() {
    if (this.refuse) throw Object.assign(new Error('not allowed'), { name: this.refuse });
    this.paused = false;
  }
  pause() {
    this.paused = true;
  }
  addEventListener(type: string, f: () => void) {
    this.listeners[type] = f;
  }
}

describe('the window’s sound player', () => {
  it('plays what the engine asks, at its volume, and says whether it started', async () => {
    FakeAudio.made = [];
    let step = 0;
    const calls = mockFetch({
      '/v1/player/next': (_m, _b, query) => {
        const after = Number(new URLSearchParams(query).get('after'));
        step++;
        if (after < 0) return { status: 200, body: { commands: [], head: 7 } };
        if (after === 7)
          return {
            status: 200,
            body: {
              commands: [
                { seq: 8, id: 'p1', kind: 'play', sound: 'air horn', volume: 40 },
                { seq: 9, id: 'p2', kind: 'play', sound: 'refused', volume: 100 },
              ],
              head: 9,
            },
          };
        if (after === 9 && step < 20) return { status: 200, body: { commands: [{ seq: 10, id: 's', kind: 'stop' }], head: 10 } };
        return { status: 200, body: { commands: [], head: after } };
      },
      '/v1/player/reports': () => ({ status: 204, body: null }),
    });
    const stop = startPlayer({ makeAudio: (url) => new FakeAudio(url, url.includes('refused') ? 'NotAllowedError' : undefined), wait: 0, retryMs: 5, gapMs: 1 });
    await settle(60);
    stop();
    const [horn, refused] = FakeAudio.made;
    expect(horn.url).toBe('/v1/sounds/air%20horn/audio');
    expect(horn.volume).toBe(0.4);
    expect(horn.paused).toBe(true); // stopped by the stop command
    expect(refused.volume).toBe(1);
    const reports = calls.filter((c) => c.path === '/v1/player/reports').map((c) => c.body);
    expect(reports).toContainEqual({ id: 'p1', ok: true });
    expect(reports).toContainEqual({ id: 'p2', ok: false, error: 'NotAllowedError: not allowed' });
    // The first ask starts from now, without waiting.
    expect(calls[0].path).toBe('/v1/player/next?after=-1&wait=0');
  });

  it(`refuses a sound when ${MAX_PLAYING} are playing`, async () => {
    FakeAudio.made = [];
    const cmds = Array.from({ length: MAX_PLAYING + 1 }, (_, i) => ({ seq: i + 1, id: `p${i}`, kind: 'play', sound: 'x', volume: 50 }));
    let sent = false;
    const calls = mockFetch({
      '/v1/player/next': (_m, _b, query) => {
        const after = Number(new URLSearchParams(query).get('after'));
        if (after < 0) return { status: 200, body: { commands: [], head: 0 } };
        if (!sent) {
          sent = true;
          return { status: 200, body: { commands: cmds, head: cmds.length } };
        }
        return { status: 200, body: { commands: [], head: after } };
      },
      '/v1/player/reports': () => ({ status: 204, body: null }),
    });
    const stop = startPlayer({ makeAudio: (url) => new FakeAudio(url), wait: 0, retryMs: 5, gapMs: 1 });
    await settle(60);
    stop();
    const reports = calls.filter((c) => c.path === '/v1/player/reports').map((c) => c.body as { ok: boolean; error?: string });
    expect(reports.filter((r) => r.ok)).toHaveLength(MAX_PLAYING);
    expect(reports.find((r) => !r.ok)?.error).toBe(`${MAX_PLAYING} sounds are already playing`);
  });

  it('keeps asking after the engine stops answering', async () => {
    let n = 0;
    mockFetch({ '/v1/player/next': () => (n++ < 2 ? { status: 503, body: { error: { code: 'engine_starting', message: 'starting' } } } : { status: 200, body: { commands: [], head: 0 } }) });
    const stop = startPlayer({ makeAudio: (url) => new FakeAudio(url), wait: 0, retryMs: 5, gapMs: 1 });
    await settle(60);
    stop();
    expect(n).toBeGreaterThan(3);
  });
});
