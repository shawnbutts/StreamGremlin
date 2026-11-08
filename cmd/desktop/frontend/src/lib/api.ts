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

export async function api<T>(method: Method, path: string, body?: unknown): Promise<T> {
  let resp: Response;
  try {
    resp = await fetch(path, {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
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
