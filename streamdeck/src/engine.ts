// Talking to StreamGremlin's API from the plugin (M7 slice 5; ADR-0042).
// The plugin's own Node process calls 127.0.0.1, never anywhere else, with
// the token pairing gave it. Every answer is ok, or why not: unreachable
// (StreamGremlin isn't running or isn't there), unpaired (the token was
// refused: pair again), refused (StreamGremlin said no, in its words), or
// unknown (a press whose answer never came: it may have happened).
import { randomUUID } from 'node:crypto';
import { join, win32 } from 'node:path';

export type Why = 'unreachable' | 'unpaired' | 'refused' | 'unknown';
export type Result<T> = { ok: true; value: T } | { ok: false; why: Why; message: string };

/** A StreamGremlin button, as a key needs it. */
export interface DeckButton {
  id: string;
  label: string;
  color: string;
  icon: string;
  hideLabel: boolean;
  /** How many running workflows a press starts. */
  starts: number;
}

/** What counter, status and switch keys show (D-123): see GET /v1/deck. */
export interface DeckView {
  status: { live: boolean | null; chat: string; obs: string; chat_locked: boolean | null };
  counters: { name: string; value: unknown }[];
  switches: { kind: SwitchKind; id?: string; label: string; on: boolean | null; gone?: boolean }[];
  /** D-124: absent from an older StreamGremlin. */
  runs?: { id: string; label: string; on: boolean; gone?: boolean; needs_viewer?: boolean }[];
  scenes?: { known: boolean; current: string; list: string[] } | null;
  countdowns?: { name: string; label: string; mode: string; running: boolean; ms: number; at: string; ended?: boolean; gone?: boolean }[];
  ads?: { known: boolean; next_ad_at?: string; snoozes: number; read_at: string; detail?: string } | null;
}

export type SwitchKind = 'workflow' | 'timer' | 'chat_lock';
export type Change = 'add' | 'take' | 'reset';

/** A switch's key in settings and messages: kind:id (chat_lock: no id). */
export const switchKey = (kind: string, id?: string) => `${kind}:${id ?? ''}`;

export const DEFAULT_ADDRESS = 'http://127.0.0.1:7430';
const API_VERSION = '1';

/** Where StreamGremlin writes its address when it starts, by system. */
export function addressFile(platform: string, env: Record<string, string | undefined>, home: string): string | undefined {
  if (platform === 'win32') return env.LOCALAPPDATA ? win32.join(env.LOCALAPPDATA, 'StreamGremlin', 'api-addr') : undefined;
  if (platform === 'darwin') return join(home, 'Library', 'Application Support', 'StreamGremlin', 'api-addr');
  return join(env.XDG_DATA_HOME ?? join(home, '.local', 'share'), 'streamgremlin', 'api-addr');
}

/** Only this machine: a field or file naming anywhere else is ignored. */
export function loopback(address: string | undefined): string | undefined {
  if (!address) return undefined;
  try {
    const u = new URL(address.trim());
    if (u.protocol !== 'http:' || !['127.0.0.1', 'localhost', '[::1]'].includes(u.hostname) || u.pathname !== '/') return undefined;
    return `${u.protocol}//${u.host}`;
  } catch {
    return undefined;
  }
}

interface Options {
  /** Reads StreamGremlin's address file. */
  readFile: () => Promise<string>;
  defaultAddress?: string;
  /** Wait between asking again after a lost answer. */
  retryMs?: number;
}

class Problem extends Error {
  constructor(
    // lost: the request may have reached StreamGremlin, but no answer came.
    readonly why: Why | 'lost',
    message: string,
  ) {
    super(message);
  }
}

export class Engine {
  constructor(private readonly opts: Options) {}

  private async call<T>(address: string, method: string, path: string, token?: string, body?: unknown, timeoutMs = 4000): Promise<{ status: number; body: T }> {
    const headers: Record<string, string> = { 'StreamGremlin-API-Version': API_VERSION };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    let res: Response;
    try {
      res = await fetch(address + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(timeoutMs) });
    } catch (e) {
      // Refused: nothing was sent. Anything else (a reset, a timeout) may
      // have reached it.
      const code = (e as { cause?: { code?: string } }).cause?.code;
      if (code === 'ECONNREFUSED') throw new Problem('unreachable', 'StreamGremlin isn’t running, or isn’t at that address.');
      throw new Problem('lost', 'StreamGremlin didn’t answer.');
    }
    const text = await res.text().catch(() => '');
    let parsed: unknown;
    try {
      parsed = text ? JSON.parse(text) : undefined;
    } catch {
      parsed = undefined;
    }
    if (res.status === 401) throw new Problem('unpaired', 'StreamGremlin doesn’t know this Stream Deck any more. Pair again.');
    if (res.status >= 400) {
      const msg = (parsed as { error?: { message?: string } } | undefined)?.error?.message ?? `StreamGremlin said no (${res.status}).`;
      throw new Problem('refused', msg);
    }
    return { status: res.status, body: parsed as T };
  }

  private async attempt<T>(f: () => Promise<T>): Promise<Result<T>> {
    try {
      return { ok: true, value: await f() };
    } catch (e) {
      if (e instanceof Problem) return { ok: false, why: e.why === 'lost' ? 'unreachable' : e.why, message: e.message, lost: e.why === 'lost' } as Result<T>;
      return { ok: false, why: 'unreachable', message: String(e) };
    }
  }

  /** Whether StreamGremlin answers at an address. */
  async alive(address: string): Promise<boolean> {
    try {
      const r = await this.call<{ status?: string }>(address, 'GET', '/v1/health', undefined, undefined, 1500);
      return r.body?.status === 'ok';
    } catch {
      return false;
    }
  }

  /** Where StreamGremlin is: the saved address, the usual one, or the one it wrote down. */
  async find(saved: string | undefined): Promise<string | undefined> {
    const tried = new Set<string>();
    const fromFile = async () => loopback(await this.opts.readFile().catch(() => ''));
    for (const next of [async () => loopback(saved), async () => loopback(this.opts.defaultAddress ?? DEFAULT_ADDRESS), fromFile]) {
      const a = await next();
      if (!a || tried.has(a)) continue;
      tried.add(a);
      if (await this.alive(a)) return a;
    }
    return undefined;
  }

  /** Swaps the code StreamGremlin shows for this deck's own token. */
  pair(address: string, code: string, name: string, requestId: string): Promise<Result<{ token: string; name: string; clientId: string }>> {
    return this.attempt(async () => {
      const r = await this.call<{ token: string; client: { id: string; name: string } }>(address, 'POST', '/v1/pair', undefined, {
        code,
        name,
        request_id: requestId,
      });
      return { token: r.body.token, name: r.body.client.name, clientId: r.body.client.id };
    });
  }

  /** Who the token is, to check it still works. */
  whoami(address: string, token: string): Promise<Result<{ name: string }>> {
    return this.attempt(async () => ({ name: (await this.call<{ name: string }>(address, 'GET', '/v1/whoami', token)).body.name }));
  }

  /** The streamer's buttons. */
  buttons(address: string, token: string): Promise<Result<DeckButton[]>> {
    return this.attempt(async () => {
      type Raw = { id: string; label: string; color: string; icon?: string; hide_label?: boolean; starts?: unknown[] };
      const r = await this.call<{ buttons: Raw[] }>(address, 'GET', '/v1/buttons', token);
      return (r.body.buttons ?? []).map((b) => ({
        id: b.id,
        label: b.label,
        color: b.color,
        icon: b.icon ?? '',
        hideLabel: !!b.hide_label,
        starts: b.starts?.length ?? 0,
      }));
    });
  }

  /** What counter, status and switch keys show. */
  deck(address: string, token: string): Promise<Result<DeckView>> {
    return this.attempt(async () => (await this.call<DeckView>(address, 'GET', '/v1/deck', token)).body);
  }

  /**
   * Asks again after a lost answer, up to twice: only for requests that are
   * safe to repeat (a request id, or setting a state). Still no answer:
   * unknown.
   */
  private async retrying<T>(f: () => Promise<T>, what: string): Promise<Result<T>> {
    for (let tries = 0; ; tries++) {
      const r = await this.attempt(f);
      if (r.ok) return r;
      if (!(r as { lost?: boolean }).lost) return { ok: false, why: r.why, message: r.message };
      if (tries >= 2) return { ok: false, why: 'unknown', message: `StreamGremlin didn’t answer, so ${what} may or may not have happened.` };
      await new Promise((ok) => setTimeout(ok, this.opts.retryMs ?? 500));
    }
  }

  /**
   * Presses a button once. The press carries a request id; when the answer
   * is lost, it's asked again with the same id, which StreamGremlin answers
   * without running anything twice (ADR-0040). Still no answer: unknown.
   */
  async press(address: string, token: string, buttonId: string): Promise<Result<{ started: number }>> {
    const requestId = `sd-${randomUUID()}`;
    const path = `/v1/buttons/${encodeURIComponent(buttonId)}/press`;
    const r = await this.retrying(async () => (await this.call<{ started?: unknown[] }>(address, 'POST', path, token, { request_id: requestId }, 8000)).body, 'the press');
    return r.ok ? { ok: true, value: { started: r.value.started?.length ?? 0 } } : r;
  }

  /** Changes a counter by one, once: a retry carries the same request id (ADR-0053). */
  count(address: string, token: string, name: string, change: Change): Promise<Result<{ value: unknown }>> {
    const requestId = `sd-${randomUUID()}`;
    const path = `/v1/deck/counters/${encodeURIComponent(name)}`;
    return this.retrying(async () => (await this.call<{ value: unknown }>(address, 'POST', path, token, { change, request_id: requestId })).body, 'the change');
  }

  /** Turns a switch on or off; asking again is harmless, as it sets a state. */
  turn(address: string, token: string, kind: SwitchKind, id: string | undefined, on: boolean): Promise<Result<{ on: boolean | null }>> {
    const path = `/v1/deck/switches/${encodeURIComponent(kind)}/${encodeURIComponent(id || '-')}`;
    return this.retrying(async () => (await this.call<{ on: boolean | null }>(address, 'PUT', path, token, { on }, 8000)).body, 'the switch');
  }

  /** Runs a workflow now, once: a retry carries the same request id (D-124). */
  run(address: string, token: string, id: string): Promise<Result<{ on: boolean }>> {
    const requestId = `sd-${randomUUID()}`;
    const path = `/v1/deck/runs/${encodeURIComponent(id)}`;
    return this.retrying(async () => {
      const b = (await this.call<{ run: { on: boolean } }>(address, 'POST', path, token, { request_id: requestId })).body;
      return { on: !!b.run?.on };
    }, 'the run');
  }

  /** Switches OBS's scene; asking again is harmless. */
  scene(address: string, token: string, scene: string): Promise<Result<{ current: string }>> {
    return this.retrying(async () => (await this.call<{ current: string }>(address, 'POST', '/v1/deck/scene', token, { scene }, 8000)).body, 'the switch');
  }

  /** Starts, pauses or resets a countdown; each sets a state, so asking again is harmless. */
  countdown(address: string, token: string, name: string, action: 'start' | 'pause' | 'reset'): Promise<Result<{ running: boolean }>> {
    const path = `/v1/deck/countdowns/${encodeURIComponent(name)}`;
    return this.retrying(async () => (await this.call<{ running: boolean }>(address, 'POST', path, token, { action })).body, 'the countdown');
  }

  /** Snoozes the next ad, once: a retry carries the same request id. */
  snooze(address: string, token: string): Promise<Result<{ snoozes: number }>> {
    const requestId = `sd-${randomUUID()}`;
    return this.retrying(async () => (await this.call<{ snoozes: number }>(address, 'POST', '/v1/deck/ads/snooze', token, { request_id: requestId }, 8000)).body, 'the snooze');
  }
}
