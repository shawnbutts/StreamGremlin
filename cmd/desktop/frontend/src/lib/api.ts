// The app's only way to reach the engine: the same API the CLI uses.
// Requests go to /v1/* on the app's own origin; the desktop shell adds the
// token in-process, so the UI never holds it.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

type Method = 'GET' | 'PUT' | 'POST' | 'DELETE';

export async function api<T>(method: Method, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let resp: Response;
  try {
    resp = await fetch(path, {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (e) {
    if (signal?.aborted) throw e;
    throw new ApiError(0, 'unreachable', "Couldn't reach the engine.");
  }
  const data = await resp.json().catch(() => undefined);
  if (!resp.ok) {
    const err = data?.error;
    throw new ApiError(resp.status, err?.code ?? 'unknown', err?.message ?? resp.statusText);
  }
  return data as T;
}

export type EngineState = 'starting' | 'ready' | 'failed';

export interface EngineInfo {
  name: string;
  state: EngineState;
  message?: string;
}

export async function engineInfo(): Promise<EngineInfo> {
  const resp = await fetch('/desktop/info');
  if (!resp.ok) throw new ApiError(resp.status, 'unknown', resp.statusText);
  return resp.json();
}

export interface Status {
  status: string;
  version: string;
  commit: string;
  started_at: string;
  uptime_seconds: number;
  data_dir: string;
  schema_version: number;
}

export const getStatus = () => api<Status>('GET', '/v1/status');

export type Theme = 'system' | 'dark' | 'light' | 'midnight';

export interface UISettings {
  theme: Theme;
}

export const getUISettings = () => api<UISettings>('GET', '/v1/settings/ui');
export const putUISettings = (s: UISettings) => api<UISettings>('PUT', '/v1/settings/ui', s);

export type SecretStoreKind = 'keyring' | 'file';
export type SecretState = 'ok' | 'missing' | 'unreadable';

export interface SecretItem {
  key: string;
  created_at: string;
  updated_at: string;
  state?: SecretState;
  detail?: string;
}

export interface SecretList {
  store: { kind: SecretStoreKind; label: string };
  checked: boolean;
  secrets: SecretItem[];
}

/** Where secrets are kept and which are saved; never their values. */
export const getSecrets = (check = false) =>
  api<SecretList>('GET', check ? '/v1/secrets?check=true' : '/v1/secrets');

/** Forget one secret. Keys are lowercase segments joined by '/', safe in a path. */
export const forgetSecret = (key: string) =>
  api<void>('DELETE', `/v1/secrets/${key.split('/').map(encodeURIComponent).join('/')}`);

export type DeliveryQuality = 'confirmed' | 'estimated' | 'synthetic';

export interface EventRecord {
  seq: number;
  event_id: string;
  type: string;
  schema_version: number;
  source: string;
  source_event_id?: string;
  occurred_at: string;
  received_at: string;
  actor?: { platform: string; id: string; login?: string; display_name?: string };
  payload: Record<string, unknown>;
  delivery_quality: DeliveryQuality;
}

export interface EventPage {
  events: EventRecord[];
  head: number;
}

/** The newest events, or with `after`, the ones after that cursor, waiting up to `wait` seconds. */
export function getEvents(opts: { after?: number; wait?: number; limit?: number }, signal?: AbortSignal) {
  const q = new URLSearchParams();
  if (opts.after !== undefined) q.set('after', String(opts.after));
  if (opts.wait) q.set('wait', String(opts.wait));
  if (opts.limit) q.set('limit', String(opts.limit));
  const qs = q.toString();
  return api<EventPage>('GET', '/v1/events' + (qs ? `?${qs}` : ''), undefined, signal);
}

export interface EventField {
  name: string;
  label: string;
  kind: 'text' | 'number';
  default: string;
  max: number;
}

export interface EventKind {
  type: string;
  label: string;
  description: string;
  sources: string[];
  sensitive: boolean;
  fields: EventField[];
}

export const getEventCatalog = () => api<{ kinds: EventKind[] }>('GET', '/v1/events/catalog');

export interface SimulateResult {
  event: EventRecord;
  duplicate: boolean;
}

/** Store a test event. The same requestId never makes a second one. */
export const simulateEvent = (type: string, fields: Record<string, string>, requestId: string) =>
  api<SimulateResult>('POST', '/v1/events/simulate', { type, fields, request_id: requestId });
