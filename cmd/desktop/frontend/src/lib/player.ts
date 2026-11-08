// The window's sound player (docs/adr/0016-sounds.md): it asks the engine
// what to play, plays it with an <audio> element at its volume, and says
// whether it started, so a workflow's trace never claims a sound that
// didn't play.
import { playerNext, reportPlay, soundURL, type PlayerCommand } from './api';

/** What the player needs of an audio element; tests pass a fake. */
export interface AudioLike {
  volume: number;
  play(): Promise<void>;
  pause(): void;
  addEventListener(type: 'ended' | 'error', f: () => void): void;
}

export const MAX_PLAYING = 4;
const MAX_SECONDS = 10 * 60;

export interface PlayerOptions {
  makeAudio?: (url: string) => AudioLike;
  /** How long one ask waits for news, in seconds. */
  wait?: number;
  /** Pause after a failed ask, in milliseconds. */
  retryMs?: number;
  /** Pause between asks, in milliseconds, so an engine that answers at
   * once can't make the window ask in a tight loop. */
  gapMs?: number;
}

/** Starts the player; the function it returns stops it. */
export function startPlayer(opts: PlayerOptions = {}): () => void {
  const makeAudio = opts.makeAudio ?? ((url: string) => new Audio(url) as AudioLike);
  const wait = opts.wait ?? 25;
  const retryMs = opts.retryMs ?? 2000;
  const gapMs = opts.gapMs ?? 50;
  const playing = new Set<AudioLike>();
  const abort = new AbortController();
  let stopped = false;

  const stopAll = () => {
    for (const a of playing) a.pause();
    playing.clear();
  };

  async function play(c: PlayerCommand) {
    if (playing.size >= MAX_PLAYING) {
      await reportPlay({ id: c.id, ok: false, error: `${MAX_PLAYING} sounds are already playing` }).catch(() => undefined);
      return;
    }
    const a = makeAudio(soundURL(c.sound ?? ''));
    a.volume = Math.min(1, Math.max(0, (c.volume ?? 80) / 100));
    const done = () => {
      a.pause();
      playing.delete(a);
    };
    a.addEventListener('ended', done);
    a.addEventListener('error', done);
    playing.add(a);
    setTimeout(done, MAX_SECONDS * 1000);
    try {
      await a.play();
      await reportPlay({ id: c.id, ok: true }).catch(() => undefined);
    } catch (e) {
      done();
      const why = e instanceof Error ? `${e.name}: ${e.message}` : String(e);
      await reportPlay({ id: c.id, ok: false, error: why }).catch(() => undefined);
    }
  }

  (async () => {
    let after = -1; // start from now: nothing queued before the window opened
    while (!stopped) {
      try {
        const r = await playerNext(after, after < 0 ? 0 : wait, abort.signal);
        for (const c of r.commands) {
          if (c.kind === 'stop') stopAll();
          else void play(c);
        }
        after = r.head;
        await new Promise((res) => setTimeout(res, gapMs));
      } catch {
        if (stopped) return;
        await new Promise((res) => setTimeout(res, retryMs));
      }
    }
  })();

  return () => {
    stopped = true;
    abort.abort();
    stopAll();
  };
}
