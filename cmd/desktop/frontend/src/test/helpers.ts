import { vi } from 'vitest';

export type Handler = (method: string, body: unknown, query: string) => { status: number; body: unknown };

/** Replace fetch with handlers keyed by path, like the desktop bridge would answer. */
export function mockFetch(routes: Record<string, Handler>) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body });
      const query = path.includes('?') ? path.slice(path.indexOf('?') + 1) : '';
      const h = routes[path] ?? routes[path.split('?')[0]];
      const res = h ? h(method, body, query) : { status: 404, body: { error: { code: 'not_found', message: 'no such route' } } };
      if (res.status === 204) return new Response(null, { status: 204 });
      return new Response(JSON.stringify(res.body), {
        status: res.status,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  return calls;
}

export const readyEngine = () => ({ status: 200, body: { name: 'StreamGremlin', state: 'ready' } });

export const runningStatus = () => ({
  status: 200,
  body: {
    status: 'running',
    version: 'dev',
    commit: 'abc1234',
    started_at: new Date(Date.now() - 65_000).toISOString(),
    uptime_seconds: 65,
    data_dir: '/tmp/sg',
    schema_version: 1,
  },
});

export async function settle(ms = 30) {
  await new Promise((r) => setTimeout(r, ms));
}

export const noSecrets = () => ({
  status: 200,
  body: { store: { kind: 'keyring', label: 'macOS Keychain' }, checked: false, secrets: [] },
});

export const noEvents = () => ({ status: 200, body: { events: [], head: 0 } });

export const eventCatalog = () => ({
  status: 200,
  body: {
    kinds: [
      {
        type: 'chat.message.received',
        label: 'Chat message',
        description: 'A viewer sent a message in chat.',
        sources: ['twitch'],
        sensitive: true,
        fields: [
          { name: 'user', label: 'Viewer', kind: 'text', default: 'TestViewer', max: 25 },
          { name: 'text', label: 'Message', kind: 'text', default: 'Hello from a test event!', max: 500 },
        ],
      },
      {
        type: 'broadcast.scene.changed',
        label: 'OBS scene changed',
        description: 'OBS switched to another scene.',
        sources: ['obs'],
        sensitive: false,
        fields: [{ name: 'scene_name', label: 'New scene', kind: 'text', default: 'Starting Soon', max: 256 }],
      },
    ],
  },
});

const account = (role: 'streamer' | 'bot', over: Record<string, unknown> = {}) => ({
  role,
  state: 'signed_out',
  scopes: [],
  missing_scopes: [],
  since: '2026-11-08T10:00:00.000Z',
  ...over,
});

/** A GET /v1/connections answer; override parts of it per test. */
export const twitchStatus = (over: { streamer?: Record<string, unknown>; bot?: Record<string, unknown>; [k: string]: unknown } = {}) => {
  const { streamer, bot, ...rest } = over;
  return {
    status: 200,
    body: {
      twitch: {
        client_id: 'abcdefghij0123456789',
        client_id_source: 'pasted',
        has_built_in: false,
        streamer: account('streamer', streamer),
        bot: account('bot', bot),
        ...rest,
      },
    },
  };
};
