import { vi } from 'vitest';

export type Handler = (method: string, body: unknown) => { status: number; body: unknown };

/** Replace fetch with handlers keyed by path, like the desktop bridge would answer. */
export function mockFetch(routes: Record<string, Handler>) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body });
      const h = routes[path];
      const res = h ? h(method, body) : { status: 404, body: { error: { code: 'not_found', message: 'no such route' } } };
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
