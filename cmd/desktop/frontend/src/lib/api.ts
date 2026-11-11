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

export type TextSize = 'small' | 'standard' | 'large' | 'larger';
export type Density = 'comfortable' | 'compact';

export interface UISettings {
  theme: Theme;
  text_size: TextSize;
  density: Density;
  /** Screens shown under "More…" in the sidebar, by id. */
  more: string[];
}

export const getUISettings = () => api<UISettings>('GET', '/v1/settings/ui');
/** Change some appearance settings; the rest keep what's saved. */
export const putUISettings = (s: Partial<UISettings>) => api<UISettings>('PUT', '/v1/settings/ui', s);

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
export function getEvents(opts: { after?: number; wait?: number; limit?: number; q?: string }, signal?: AbortSignal) {
  const q = new URLSearchParams();
  if (opts.after !== undefined) q.set('after', String(opts.after));
  if (opts.wait) q.set('wait', String(opts.wait));
  if (opts.limit) q.set('limit', String(opts.limit));
  if (opts.q) q.set('q', opts.q);
  const qs = q.toString();
  return api<EventPage>('GET', '/v1/events' + (qs ? `?${qs}` : ''), undefined, signal);
}

export interface EventField {
  name: string;
  label: string;
  kind: 'text' | 'number' | 'choice';
  default: string;
  max: number;
  choices?: string[];
  /** The payload path the field sets, such as chatter.role. */
  path?: string;
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

export type TwitchRole = 'streamer' | 'bot';
export type AccountState = 'not_configured' | 'signed_out' | 'signing_in' | 'connected' | 'needs_sign_in' | 'unknown';

export interface PendingSignIn {
  user_code: string;
  verification_uri: string;
  expires_at: string;
}

export interface AccountStatus {
  role: TwitchRole;
  state: AccountState;
  login?: string;
  user_id?: string;
  scopes: string[];
  missing_scopes: string[];
  detail?: string;
  since: string;
  checked_at?: string;
  sign_in?: PendingSignIn;
}

export interface TwitchStatus {
  client_id: string;
  client_id_source: 'none' | 'built_in' | 'pasted';
  has_built_in: boolean;
  test_server?: string;
  streamer: AccountStatus;
  bot: AccountStatus;
  chat: ChatStatus;
  /** The title and category, when Twitch has said. */
  channel?: { title: string; category: string; known: boolean };
}

/** Whether Twitch sends one kind of channel event, and why not. */
export interface FeedStatus {
  type: string;
  label: string;
  ok: boolean;
  problem?: string;
}

export interface ChatStatus {
  state: 'off' | 'connecting' | 'connected' | 'disconnected';
  detail?: string;
  since: string;
  bot_moderator?: boolean;
  feeds?: FeedStatus[];
}

export interface SendResult {
  message_id: string;
  sent_as: TwitchRole;
  login: string;
  note?: string;
}

/** Send to your Twitch chat. Never retried: a repeat could post twice. */
export const sendChat = (text: string, as: '' | TwitchRole) =>
  api<SendResult>('POST', '/v1/chat/messages', { text, as });

export const getConnections = (signal?: AbortSignal) =>
  api<{ twitch: TwitchStatus; obs: OBSStatus }>('GET', '/v1/connections', undefined, signal);

export const setTwitchClientId = (clientId: string) =>
  api<{ twitch: TwitchStatus }>('PUT', '/v1/connections/twitch/client-id', { client_id: clientId });

export const startTwitchSignIn = (role: TwitchRole) =>
  api<PendingSignIn>('POST', `/v1/connections/twitch/${role}/sign-in`);

export const cancelTwitchSignIn = (role: TwitchRole) => api<void>('DELETE', `/v1/connections/twitch/${role}/sign-in`);

export const twitchSignOut = (role: TwitchRole) => api<void>('POST', `/v1/connections/twitch/${role}/sign-out`);

/** Open an allow-listed page (twitch.tv/activate, dev.twitch.tv) in the system browser. */
export async function openInBrowser(url: string): Promise<void> {
  const resp = await fetch('/desktop/open-url', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  });
  if (!resp.ok) {
    const data = await resp.json().catch(() => undefined);
    throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
  }
}

/**
 * Save text to a file where the streamer chooses, with the system's own save
 * dialog (never <a download>). Resolves to the path, or '' when cancelled.
 */
export async function saveFile(name: string, text: string): Promise<string> {
  const resp = await fetch('/desktop/save-file', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, text }),
  });
  const data = await resp.json().catch(() => undefined);
  if (!resp.ok) throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
  return typeof data?.saved === 'string' ? data.saved : '';
}

export type OBSState = 'off' | 'connecting' | 'connected' | 'disconnected' | 'needs_action';

export interface OBSStatus {
  url: string;
  enabled: boolean;
  has_password: boolean;
  state: OBSState;
  detail?: string;
  since: string;
  obs_version?: string;
  websocket_version?: string;
  scene?: string;
  scene_set_by?: string;
}

export interface SceneItem {
  id: number;
  source: string;
  visible: boolean;
  is_group: boolean;
}

export interface Snapshot {
  id: string;
  key: string;
  previous_scene: string;
  target_scene: string;
  switched: boolean;
  status: 'open' | 'restored' | 'skipped' | 'failed' | 'released';
  reason?: string;
  message?: string;
  would_restore?: boolean;
}

/** Save OBS's settings. The password is write-only: omit it to keep the saved one. */
export const configureOBS = (url: string, enabled: boolean, password?: string, clearPassword = false) =>
  api<OBSStatus>('PUT', '/v1/connections/obs', {
    url,
    enabled,
    ...(password ? { password } : {}),
    ...(clearPassword ? { clear_password: true } : {}),
  });

export const getOBSScenes = () => api<{ current: string; scenes: string[] }>('GET', '/v1/obs/scenes');
export const getSceneItems = (scene: string) =>
  api<{ items: SceneItem[] }>('GET', '/v1/obs/scene-items?scene=' + encodeURIComponent(scene));
export const switchScene = (scene: string) => api<{ scene: string }>('POST', '/v1/obs/scene', { scene });
export const setSourceVisible = (scene: string, source: string, visible: boolean) =>
  api<{ visible: boolean }>('PUT', '/v1/obs/scene-items/visibility', { scene, source, visible });
export const getSnapshots = () => api<{ snapshots: Snapshot[] }>('GET', '/v1/obs/snapshots');
export const takeSnapshot = (key: string, scene: string) => api<Snapshot>('POST', '/v1/obs/snapshots', { key, scene });
export const restoreSnapshot = (key: string) =>
  api<Snapshot>('POST', `/v1/obs/snapshots/${encodeURIComponent(key)}/restore`);
export const releaseSnapshot = (key: string) =>
  api<Snapshot>('POST', `/v1/obs/snapshots/${encodeURIComponent(key)}/release`);

export interface Trigger {
  type: string;
  sources?: string[];
  delivery_quality?: DeliveryQuality[];
  where?: Record<string, unknown>;
}

export type JobStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'cancelled'
  | 'interrupted'
  | 'rejected'
  | 'dropped'
  | 'superseded';

export interface Job {
  id: string;
  workflow_id: string;
  version: number;
  event_id: string;
  event_seq: number;
  status: JobStatus;
  reason?: string;
  dry_run: boolean;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface WorkflowSummary {
  id: string;
  name: string;
  /** Its own switch. */
  enabled: boolean;
  /** Its group ("" for none), and that group's switch. */
  group?: string;
  group_enabled?: boolean;
  /** It runs: on, with its group on. */
  running?: boolean;
  version: number;
  triggers: Trigger[];
  updated_at: string;
  deleted_at?: string;
  last_job?: Job;
}

export interface StepRecord {
  path: string;
  step_id?: string;
  kind: string;
  action?: string;
  status: 'ok' | 'failed' | 'skipped';
  reason?: string;
  error?: string;
  input?: unknown;
  output?: unknown;
  started_at: string;
  finished_at: string;
}

export interface ActionParam {
  name: string;
  label: string;
  type: string;
  choices?: string[];
  required: boolean;
  pattern?: string;
  pattern_hint?: string;
  default?: unknown;
  /** Written out, never computed (which workflow to run). */
  literal?: boolean;
  /** How the editor shows it: message, scene, source, workflow, variable or value. */
  editor?: string;
  /** What leaving an optional parameter unset does, in words. */
  default_hint?: string;
  /** The most bytes a text value may have (a Lua script's 64 KiB). */
  max_length?: number;
}

export interface ActionInfo {
  name: string;
  label: string;
  description: string;
  group: string;
  external: boolean;
  /** Changes saved variables; a test event changes them for that run only. */
  saves_state?: boolean;
  /** Behind "More steps…" in the editor. */
  more?: boolean;
  /** The add-on (a Lua plugin) it comes from, by id; absent for StreamGremlin's own. */
  add_on?: string;
  params: ActionParam[];
}

/** Something a message can include: {token} on screen, ${expr} stored. */
export interface PaletteValue {
  token: string;
  label: string;
  expr: string;
}

export interface MatchField {
  path: string;
  label: string;
  kind: 'command' | 'who' | 'text' | 'scene' | 'source' | 'on_off' | 'timer';
  help?: string;
}

export interface TriggerEntry {
  type: string;
  label: string;
  category: string;
  help: string;
  more?: boolean;
  match: MatchField[];
  values: PaletteValue[];
}

export interface Palette {
  triggers: TriggerEntry[];
  actions: ActionInfo[];
}

export const getPalette = () => api<Palette>('GET', '/v1/palette');

export interface Problem {
  path: string;
  message: string;
}

/** A save refused because of problems in the definition, each at its JSON path. */
export class ProblemsError extends ApiError {
  constructor(readonly problems: Problem[]) {
    super(400, 'invalid_request', 'the workflow has problems');
  }
}

export const getWorkflows = () => api<{ workflows: WorkflowSummary[] }>('GET', '/v1/workflows');
export const getDeletedWorkflows = () => api<{ workflows: WorkflowSummary[] }>('GET', '/v1/workflows?deleted=true');
export const getWorkflow = (id: string) =>
  api<{ workflow: WorkflowSummary; definition: Record<string, unknown> }>('GET', `/v1/workflows/${encodeURIComponent(id)}`);
export const deleteWorkflow = (id: string) => api<void>('DELETE', `/v1/workflows/${encodeURIComponent(id)}`);
export const restoreWorkflow = (id: string) => api<WorkflowSummary>('POST', `/v1/workflows/${encodeURIComponent(id)}/restore`);
export const setWorkflowEnabled = (id: string, enabled: boolean) =>
  api<WorkflowSummary>('PUT', `/v1/workflows/${encodeURIComponent(id)}/enabled`, { enabled });
export const getActions = () => api<{ actions: ActionInfo[] }>('GET', '/v1/actions');
export const getJobs = (workflow?: string) =>
  api<{ jobs: Job[] }>('GET', '/v1/jobs?limit=50' + (workflow ? '&workflow=' + encodeURIComponent(workflow) : ''));
export const getJob = (id: string) => api<{ job: Job; steps: StepRecord[] }>('GET', `/v1/jobs/${encodeURIComponent(id)}`);
export const cancelJob = (id: string) => api<void>('POST', `/v1/jobs/${encodeURIComponent(id)}/cancel`);

/** Save a definition given as JSON text. Problems come back as a ProblemsError. */
export async function saveWorkflow(text: string): Promise<{ workflow: WorkflowSummary; changed: boolean }> {
  let id = '';
  try {
    id = JSON.parse(text)?.id ?? '';
  } catch (e) {
    throw new ProblemsError([{ path: '$', message: `not JSON: ${(e as Error).message}` }]);
  }
  if (typeof id !== 'string' || !id) throw new ProblemsError([{ path: '$.id', message: 'the workflow needs an id' }]);
  const resp = await fetch(`/v1/workflows/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: text,
  }).catch(() => {
    throw new ApiError(0, 'unreachable', "Couldn't reach the engine.");
  });
  const data = await resp.json().catch(() => undefined);
  if (resp.ok) return data;
  if (Array.isArray(data?.problems)) throw new ProblemsError(data.problems);
  throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
}

export type VarScope = 'global' | 'viewer' | 'workflow';

/** A saved variable. owner is '' for global, <platform>:<user id> for a viewer, or a workflow id. */
export interface Variable {
  scope: VarScope;
  owner: string;
  owner_label?: string;
  name: string;
  value: unknown;
  updated_at: string;
  updated_by: string;
}

export interface VarKey {
  scope: VarScope;
  owner: string;
  name: string;
}

export const getVariables = (opts: { scope?: VarScope; q?: string } = {}) => {
  const q = new URLSearchParams();
  if (opts.scope) q.set('scope', opts.scope);
  if (opts.q) q.set('q', opts.q);
  return api<{ variables: Variable[] }>('GET', `/v1/variables?${q}`);
};
export const putVariable = (k: VarKey, value: unknown) =>
  api<Variable>('PUT', '/v1/variables', { scope: k.scope, owner: k.owner, name: k.name, value });
export const deleteVariable = (k: VarKey) =>
  api<void>('DELETE', `/v1/variables?${new URLSearchParams({ scope: k.scope, owner: k.owner, name: k.name })}`);

/** One workflow that answers a chat command. */
export interface CommandOwner {
  owner: string;
  name: string;
  enabled: boolean;
  sources?: string[];
  roles?: string[];
}

/** A chat command and what answers it. conflict: two that are on overlap. */
export interface CommandEntry {
  command: string;
  owners: CommandOwner[];
  conflict: boolean;
}

export const getCommands = () => api<{ commands: CommandEntry[] }>('GET', '/v1/commands');

/** One Get set up step, ticked from real state. */
export interface SetupStep {
  id: string;
  label: string;
  help: string;
  done: boolean;
  optional?: boolean;
  screen: string;
}

/** Something that needs the streamer: a problem, an unknown state, or a tip. */
export interface AttentionItem {
  level: 'problem' | 'unknown' | 'tip';
  message: string;
  screen: string;
  /** Something Home can do about it in place. */
  action?: 'mark_failures_seen';
}

export interface Overview {
  checklist: SetupStep[];
  set_up: boolean;
  attention: AttentionItem[];
  workflows: { total: number; on: number; tested: boolean; answered: boolean; failed_24h: number };
}

export const getOverview = () => api<Overview>('GET', '/v1/overview');
/** Mark the failed runs so far as seen; the runs and traces are kept. */
export const markFailuresSeen = () => api<{ seen_at: string }>('POST', '/v1/overview/failures-seen');

/** A value to fill in before saving a template, at a JSON path. */
export interface Blank {
  path: string;
  hint: string;
}

/** A ready-made workflow from the gallery. Nothing is added until it's saved. */
export interface Template {
  id: string;
  title: string;
  category: string;
  description: string;
  starter: boolean;
  blanks: Blank[];
  workflow: Record<string, unknown>;
  /** The add-on whose template it is (its category is the add-on's name), or absent for StreamGremlin's own. */
  add_on?: { id: string; name: string; publisher: string; enabled: boolean };
}

export const getTemplates = () => api<{ templates: Template[]; categories: string[] }>('GET', '/v1/templates');

export type BackupKind = 'manual' | 'scheduled' | 'pre-restore' | 'pre-migration';

export interface BackupInfo {
  name: string;
  kind: BackupKind;
  created_at: string;
  size: number;
  schema_version?: number;
  app_version?: string;
  problem?: string;
}

export interface RestoreResult {
  at: string;
  from: string;
  ok: boolean;
  error?: string;
  pre_restore?: string;
  kept_aside?: string;
}

export interface MaintenanceSettings {
  backup_every_hours: number;
  backup_keep: number;
  events_days: number;
  runs_days: number;
}

export interface SweepResult {
  at: string;
  events: number;
  runs: number;
  error?: string;
}

export interface Backups {
  backups: BackupInfo[];
  pending?: string;
  last_restore?: RestoreResult;
  settings: MaintenanceSettings;
  status: { last_backup_at?: string; last_backup_error?: string; next_backup_at?: string; last_sweep?: SweepResult };
}

export const getBackups = () => api<Backups>('GET', '/v1/backups');
export const createBackup = () => api<BackupInfo>('POST', '/v1/backups');
export const verifyBackup = (name: string) => api<{ ok: boolean }>('POST', `/v1/backups/${encodeURIComponent(name)}/verify`);
export const stageRestore = (name: string) => api<{ pending: string }>('POST', `/v1/backups/${encodeURIComponent(name)}/restore`);
export const cancelRestore = (name: string) => api<void>('DELETE', `/v1/backups/${encodeURIComponent(name)}/restore`);
export const deleteBackup = (name: string) => api<void>('DELETE', `/v1/backups/${encodeURIComponent(name)}`);
export const putMaintenance = (s: MaintenanceSettings) => api<MaintenanceSettings>('PUT', '/v1/settings/maintenance', s);
export const sweepNow = () => api<SweepResult>('POST', '/v1/maintenance/sweep');

/** Restart the app's engine: a staged restore is applied as it starts. */
export async function restartEngine(): Promise<void> {
  const resp = await fetch('/desktop/restart-engine', { method: 'POST' });
  if (!resp.ok) {
    const data = await resp.json().catch(() => undefined);
    throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
  }
}

/** What a timer is set to do (docs/adr/0013-timers.md). */
export interface TimerInput {
  id: string;
  name: string;
  enabled: boolean;
  /** Every so often; with up_to_seconds, at random between the two. */
  every_seconds?: number;
  up_to_seconds?: number;
  /** Or a five-field cron schedule, in the computer's local time. */
  cron?: string;
  /** Chat lines from viewers to wait for since it last went off. */
  chat_lines: number;
  live_only: boolean;
  once: boolean;
}

export interface Timer extends TimerInput {
  counter: number;
  last_fired_at?: string;
  created_at: string;
  updated_at: string;
}

export interface TimerStatus extends Timer {
  next_at?: string;
  /** off; chat: its time came and it waits for chat lines; never: the schedule matches no date. */
  waiting?: 'off' | 'chat' | 'never';
  lines?: number;
}

export const getTimers = () => api<{ timers: TimerStatus[]; live?: boolean }>('GET', '/v1/timers');
export function saveTimer(t: TimerInput) {
  // Only what can be set: never the count or the times.
  const body: TimerInput = {
    id: t.id,
    name: t.name,
    enabled: t.enabled,
    chat_lines: t.chat_lines,
    live_only: t.live_only,
    once: t.once,
  };
  if (t.cron) body.cron = t.cron;
  else {
    body.every_seconds = t.every_seconds;
    if (t.up_to_seconds) body.up_to_seconds = t.up_to_seconds;
  }
  return api<TimerStatus>('PUT', `/v1/timers/${encodeURIComponent(t.id)}`, body);
}
export const deleteTimer = (id: string) => api<void>('DELETE', `/v1/timers/${encodeURIComponent(id)}`);
export const fireTimer = (id: string) => api<{ timer: Timer }>('POST', `/v1/timers/${encodeURIComponent(id)}/fire`);

/** A numbered quote (docs/adr/0014-quotes.md). Numbers are never reused. */
export interface Quote {
  id: number;
  text: string;
  added_by: string;
  added_by_id?: string;
  platform?: string;
  category?: string;
  created_at: string;
  updated_at: string;
}

export interface QuoteInput {
  text: string;
  added_by: string;
  category: string;
}

export function getQuotes(q?: string) {
  const query = q ? `?q=${encodeURIComponent(q)}` : '';
  return api<{ quotes: Quote[]; count: number; next: number }>('GET', `/v1/quotes${query}`);
}
/** requestId makes a retried add return the first quote, not add another. */
export const addQuote = (q: QuoteInput, requestId: string) => api<Quote>('POST', '/v1/quotes', { ...q, request_id: requestId });
export const editQuote = (id: number, q: QuoteInput) =>
  api<Quote>('PUT', `/v1/quotes/${id}`, { text: q.text, added_by: q.added_by, category: q.category });
export const deleteQuote = (id: number) => api<void>('DELETE', `/v1/quotes/${id}`);

/** A sound in the library (docs/adr/0016-sounds.md). */
export interface Sound {
  name: string;
  format: 'mp3' | 'wav' | 'ogg' | 'm4a';
  size: number;
  sha256: string;
  volume: number;
  created_at: string;
  updated_at: string;
}

export const getSounds = () => api<{ sounds: Sound[]; player: boolean }>('GET', '/v1/sounds');
/** The audio's URL, for the player and for previews. */
export const soundURL = (name: string) => `/v1/sounds/${encodeURIComponent(name)}/audio`;

/** Add a sound: the file's bytes go as they are; the engine checks them.
 * They're read first and sent as bytes: macOS's webview drops a File or
 * Blob sent as a request body to the app's own scheme, so the engine got
 * nothing (found in the slice 4 live check). */
export async function addSound(name: string, file: Blob, volume: number): Promise<Sound> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let resp: Response;
  try {
    resp = await fetch(`/v1/sounds/${encodeURIComponent(name)}?volume=${volume}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: bytes,
    });
  } catch {
    throw new ApiError(0, 'unreachable', "Couldn't reach the engine.");
  }
  const data = await resp.json().catch(() => undefined);
  if (!resp.ok) throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
  return data as Sound;
}

export const setSoundVolume = (name: string, volume: number) => api<Sound>('PUT', `/v1/sounds/${encodeURIComponent(name)}`, { volume });
export const deleteSound = (name: string) => api<void>('DELETE', `/v1/sounds/${encodeURIComponent(name)}`);
export const playSound = (name: string, volume?: number) =>
  api<{ played: boolean }>('POST', `/v1/sounds/${encodeURIComponent(name)}/play`, volume === undefined ? undefined : { volume });
export const stopSounds = () => api<void>('POST', '/v1/player/stop');

/** One thing for the window's player to do. */
export interface PlayerCommand {
  seq: number;
  id: string;
  kind: 'play' | 'stop';
  sound?: string;
  volume?: number;
}

export const playerNext = (after: number, wait: number, signal?: AbortSignal) =>
  api<{ commands: PlayerCommand[]; head: number }>('GET', `/v1/player/next?after=${after}&wait=${wait}`, undefined, signal);
export const reportPlay = (r: { id: string; ok: boolean; error?: string }) => api<void>('POST', '/v1/player/reports', r);

// Overlays (docs/adr/0017-alert-overlays.md): each is one OBS browser source.

export type OverlayPosition = 'top-left' | 'top' | 'top-right' | 'center' | 'bottom-left' | 'bottom' | 'bottom-right';

export type OverlayFont = 'system' | 'rounded' | 'serif' | 'mono' | 'condensed' | 'handwritten' | 'custom';

export interface OverlayLook {
  position: OverlayPosition;
  animation: 'slide' | 'pop' | 'unfold';
  seconds: number;
  /** How long the animation takes, percent of the usual (50 to 300; higher is slower). */
  pace: number;
  // The box.
  background: string;
  /** A gradient's second color, or "" for one color. */
  background2: string;
  gradient_angle: number;
  opacity: number;
  radius: number;
  border_width: number;
  border_color: string;
  shadow: number;
  shadow_color: string;
  // The words.
  font: OverlayFont;
  /** A font installed on this computer, for "custom". */
  font_name: string;
  text_size: number;
  text_color: string;
  title_color: string;
  text_outline: number;
  outline_color: string;
  text_shadow: number;
  // Giveaway and wheel overlays: a sound from the library as a roll or spin
  // starts and as it lands ('' for none), with volumes 0 to 100.
  start_sound?: string;
  start_volume?: number;
  end_sound?: string;
  end_volume?: number;
  // A moment after it lands, as the result shows: a cheer.
  cheer_sound?: string;
  cheer_volume?: number;
  // Wheel overlays: a look other than Classic (docs/adr/0036-packages-plugins-and-themes.md),
  // by id, with its settings; unset ones take the look's defaults.
  // "" (Classic) is sent, not left out: a look given in part keeps the rest.
  theme?: string;
  theme_settings?: Record<string, string | boolean | number>;
}

export interface Overlay {
  name: string;
  // A giveaway overlay shows the running giveaway (docs/adr/0029-giveaway-overlay.md).
  kind: 'alert' | 'giveaway' | 'wheel';
  look: OverlayLook;
  created_at: string;
  updated_at: string;
  /** Pages open in OBS (or a browser). */
  pages: number;
  /** The app's own previews. */
  previews: number;
}

/** A look a wheel overlay can have (docs/adr/0036-packages-plugins-and-themes.md). */
export interface OverlayTheme {
  id: string;
  name: string;
  description: string;
  // StreamGremlin's tests check it shows the real odds.
  odds_checked: boolean;
  settings: ThemeSetting[];
  /** An installed look's version and publisher (as stated, not verified). */
  version?: string;
  publisher?: string;
}

/** One of a look's settings. */
export interface ThemeSetting {
  key: string;
  label: string;
  kind: 'text' | 'switch' | 'colour' | 'number' | 'choice';
  default: string | boolean | number;
  max?: number;
  min?: number;
  options?: string[];
  hint?: string;
}

export interface OverlayList {
  overlays: Overlay[];
  /** Where overlays are served; "" when the server is off. */
  server: string;
  server_note?: string;
  positions: OverlayPosition[];
  animations: ('slide' | 'pop' | 'unfold')[];
  fonts: OverlayFont[];
  default_look: OverlayLook;
  // Looks a wheel overlay can have besides Classic.
  themes: OverlayTheme[];
}

const overlayPath = (name: string) => `/v1/overlays/${encodeURIComponent(name)}`;

export const getOverlays = () => api<OverlayList>('GET', '/v1/overlays');
export const addOverlay = (name: string, look?: OverlayLook, kind: Overlay['kind'] = 'alert') =>
  api<{ overlay: Overlay; url?: string }>('POST', '/v1/overlays', { name, kind, look });
export const setOverlayLook = (name: string, look: OverlayLook) => api<Overlay>('PUT', overlayPath(name), { look });
export const deleteOverlay = (name: string) => api<{ deleted: boolean }>('DELETE', overlayPath(name));
/** The overlay's private link, to paste into OBS. */
export const getOverlayLink = (name: string) =>
  api<{ name: string; url: string }>('GET', `/v1/overlays/${encodeURIComponent(name)}/link`);
/** A new link; the old one stops working at once. */
export const newOverlayLink = (name: string) =>
  api<{ name: string; url: string }>('POST', `/v1/overlays/${encodeURIComponent(name)}/link`);
/**
 * A sample alert, shown in OBS; or, with preview, in the app's previews
 * only, in look if given (unsaved: OBS keeps the saved look).
 */
export const testOverlay = (name: string, preview = false, look?: OverlayLook) =>
  api<{ shown: boolean; previews?: number }>(
    'POST',
    preview ? `/v1/overlays/${encodeURIComponent(name)}/test?preview=1` : `/v1/overlays/${encodeURIComponent(name)}/test`,
    preview && look ? { look } : undefined,
  );

// Channel point rewards (docs/adr/0018-channel-point-rewards.md). Twitch is
// the only record; StreamGremlin can change only the rewards it made.

export interface Reward {
  id: string;
  title: string;
  prompt: string;
  cost: number;
  background_color: string;
  enabled: boolean;
  paused: boolean;
  in_stock: boolean;
  input_required: boolean;
  skip_queue: boolean;
  max_per_stream: number;
  max_per_user_per_stream: number;
  cooldown_seconds: number;
  /** Made in StreamGremlin: it can be changed, and its redemptions handled. */
  managed: boolean;
}

/** What to set; fields left out stay as they are. */
export type RewardChange = Partial<Omit<Reward, 'id' | 'managed' | 'in_stock'>>;

export interface RewardList {
  /** ok, or why there's no list. */
  state: 'ok' | 'not_signed_in' | 'needs_permission' | 'no_channel_points' | 'unknown';
  detail?: string;
  rewards: Reward[];
  can_manage: boolean;
  max: number;
}

export interface Redemption {
  id: string;
  reward_id: string;
  reward: string;
  cost: number;
  user_login: string;
  user_name: string;
  input: string;
  status: string;
  redeemed_at: string;
}

export const getRewards = () => api<RewardList>('GET', '/v1/rewards');
export const addReward = (r: RewardChange) => api<Reward>('POST', '/v1/rewards', r);
export const changeReward = (id: string, r: RewardChange) => api<Reward>('PUT', `/v1/rewards/${encodeURIComponent(id)}`, r);
export const deleteReward = (id: string) => api<{ deleted: boolean }>('DELETE', `/v1/rewards/${encodeURIComponent(id)}`);
export const getRedemptions = (id: string) =>
  api<{ redemptions: Redemption[] }>('GET', `/v1/rewards/${encodeURIComponent(id)}/redemptions`);
export const setRedemption = (rewardID: string, id: string, action: 'done' | 'refund') =>
  api<{ redemption: Redemption; repeated: boolean }>(
    'POST',
    `/v1/rewards/${encodeURIComponent(rewardID)}/redemptions/${encodeURIComponent(id)}`,
    { action },
  );

// Workflow groups (docs/adr/0020-workflow-groups.md).

export interface WorkflowGroup {
  name: string;
  enabled: boolean;
  workflows: number;
  on: number;
}

export const getWorkflowGroups = () => api<{ groups: WorkflowGroup[] }>('GET', '/v1/workflow-groups');
/** Switch a group; each workflow keeps its own switch. */
export const setWorkflowGroupEnabled = (name: string, enabled: boolean) =>
  api<{ groups: WorkflowGroup[] }>('PUT', `/v1/workflow-groups/${encodeURIComponent(name)}/enabled`, { enabled });
export const renameWorkflowGroup = (name: string, to: string) =>
  api<{ name: string }>('PUT', `/v1/workflow-groups/${encodeURIComponent(name)}`, { name: to });

// First Words (docs/adr/0021-first-words.md).

export interface FirstWordsStatus {
  /** When this stream began, for first words. */
  since: string;
  why: 'stream start' | 'reset' | '12 hours';
  /** Viewers who've chatted since. */
  viewers: number;
}

export const getFirstWords = () => api<FirstWordsStatus>('GET', '/v1/first-words');
/** Start over: everyone's next message counts as their first words again. */
export const resetFirstWords = () => api<FirstWordsStatus>('POST', '/v1/first-words/reset');

// Polls and predictions (docs/adr/0023-polls-and-predictions.md).

export interface PollChoice {
  id: string;
  title: string;
  votes: number;
  channel_points_votes: number;
}

export interface Poll {
  id: string;
  title: string;
  choices: PollChoice[];
  status: string;
  seconds: number;
  channel_points_per_vote: number;
  started_at: string;
  ended_at?: string;
}

export interface Outcome {
  id: string;
  title: string;
  color: string;
  users: number;
  channel_points: number;
}

export interface Prediction {
  id: string;
  title: string;
  outcomes: Outcome[];
  status: string;
  seconds: number;
  winning_outcome_id?: string;
  created_at: string;
}

export interface PollsView {
  state: 'ok' | 'not_signed_in' | 'needs_permission' | 'no_channel_points' | 'unknown';
  detail?: string;
  poll: Poll | null;
  prediction: Prediction | null;
}

export const getPolls = () => api<PollsView>('GET', '/v1/polls');
export const startPoll = (p: { title: string; choices: string[]; seconds: number; channel_points_per_vote: number }) =>
  api<Poll>('POST', '/v1/polls', p);
export const endPoll = (archive: boolean) => api<Poll>('POST', '/v1/polls/end', { archive });
export const startPrediction = (p: { title: string; outcomes: string[]; seconds: number }) => api<Prediction>('POST', '/v1/predictions', p);
export const endPrediction = (how: 'lock' | 'resolve' | 'cancel', winner = '') =>
  api<{ prediction: Prediction; repeated: boolean }>('POST', '/v1/predictions/end', { how, winner });

// Ads (docs/adr/0024-ads.md): what was asked and what Twitch said happened,
// kept apart. A break's end is always an estimate.

export interface AdSchedule {
  snooze_count: number;
  snooze_refresh_at?: string;
  next_ad_at?: string;
  duration: number;
  last_ad_at?: string;
  preroll_free_seconds: number;
}

export interface AdRequest {
  id: string;
  action: 'commercial' | 'snooze';
  length?: number;
  requested_by: string;
  status: 'pending' | 'accepted' | 'refused' | 'unknown';
  reason?: string;
  message?: string;
  accepted_length?: number;
  retry_until?: string;
  next_ad_at?: string;
  snooze_count?: number;
  confirmed_by?: '' | 'event' | 'schedule';
  created_at: string;
}

export interface AdBreak {
  key: string;
  started_at: string;
  duration_seconds: number;
  is_automatic: boolean;
  request_id?: string;
  end_estimated_at: string;
}

export interface AdsView {
  state: 'ok' | 'not_signed_in' | 'needs_permission' | 'no_channel_points' | 'unknown';
  detail?: string;
  schedule: AdSchedule | null;
  requests: AdRequest[];
  breaks: AdBreak[];
}

export const getAds = () => api<AdsView>('GET', '/v1/ads');
/** opKey makes a repeat harmless: the same key is never sent to Twitch twice. */
export const startCommercial = (length: number, opKey: string) =>
  api<AdRequest>('POST', '/v1/ads/commercial', { length, op_key: opKey });
export const snoozeAd = (opKey: string) => api<AdRequest>('POST', '/v1/ads/snooze', { op_key: opKey });

// Giveaways (docs/adr/0025-giveaways.md).

// Who may enter (docs/adr/0026-giveaway-entry-rules.md); each is off at 0.
export interface GiveawayRules {
  followers?: boolean;
  follow_days?: number;
  account_days?: number;
  sub_months?: number;
  messages?: number;
  win_cooldown_days?: number;
}

export interface GiveawaySettings {
  title: string;
  command?: string;
  winners?: number;
  minutes?: number;
  claim_seconds?: number;
  claim_command?: string;
  subscribers_only?: boolean;
  rules?: GiveawayRules;
  enter_by_chat?: boolean;
  // Chances each subscriber holds, 2 to 10 (docs/adr/0027-giveaway-chances.md); 1: the same as everyone.
  subscriber_chances?: number;
  // Draw now from everyone who chatted in this many minutes, 1 to 60 (docs/adr/0028-giveaway-from-chat.md).
  from_chat_minutes?: number;
  practice?: boolean;
}

export interface Giveaway {
  id: string;
  title: string;
  command: string;
  claim_command?: string;
  winners: number;
  claim_seconds: number;
  subscribers_only: boolean;
  rules: GiveawayRules;
  enter_by_chat: boolean;
  subscriber_chances: number;
  from_chat_minutes?: number;
  practice: boolean;
  state: 'open' | 'closed' | 'ended' | 'cancelled';
  opened_by: string;
  opened_at: string;
  closes_at?: string;
  closed_at?: string;
  ended_at?: string;
  entry_count: number;
  fingerprint?: string;
}

export interface GiveawayEntry {
  seq: number;
  platform: string;
  user_id: string;
  login: string;
  display_name: string;
  entered_at: string;
  // Tickets the entry holds (1 unless subscribers were given more).
  chances?: number;
  // What the entry was checked against, when rules apply.
  qualified?: { followed_at?: string; account_created_at?: string; sub_months?: number; messages?: number };
}

export interface GiveawayDraw {
  id: string;
  n: number;
  entry_seq: number;
  pick_index: number;
  candidates: number;
  outcome: 'won' | 'pending_claim' | 'claimed' | 'forfeited' | 'set_aside';
  claim_until?: string;
  drawn_at: string;
  settled_at?: string;
  settled_by?: string;
  winner: GiveawayEntry;
}

export interface GiveawayMessage {
  id: number;
  text: string;
  state: 'queued' | 'sending' | 'sent' | 'failed' | 'unknown' | 'practice';
  detail?: string;
  created_at: string;
}

export interface GiveawayDetail extends Giveaway {
  entries: GiveawayEntry[];
  draws: GiveawayDraw[];
  refusals: { login: string; display_name: string; reason: string }[];
  messages: GiveawayMessage[];
}

export const getGiveaways = () => api<{ current: GiveawayDetail | null; recent: Giveaway[] }>('GET', '/v1/giveaways');
export const getGiveaway = (id: string) => api<GiveawayDetail>('GET', `/v1/giveaways/${encodeURIComponent(id)}`);
export const openGiveaway = (s: GiveawaySettings) => api<Giveaway>('POST', '/v1/giveaways', s);
const gid = (id: string) => encodeURIComponent(id);
export const closeGiveaway = (id: string) => api<Giveaway>('POST', `/v1/giveaways/${gid(id)}/close`);
export const drawGiveaway = (id: string, count = 0) => api<{ draws: GiveawayDraw[] }>('POST', `/v1/giveaways/${gid(id)}/draw`, { count });
export const setAsideWinner = (id: string, drawId: string) =>
  api<{ draws: GiveawayDraw[] }>('POST', `/v1/giveaways/${gid(id)}/set-aside`, { draw_id: drawId });
export const endGiveaway = (id: string) => api<Giveaway>('POST', `/v1/giveaways/${gid(id)}/end`);
export const cancelGiveaway = (id: string) => api<Giveaway>('POST', `/v1/giveaways/${gid(id)}/cancel`);

// Wheels (docs/adr/0030-wheels.md).

export interface WheelChoice {
  label: string;
  weight: number;
  color?: string;
}

export interface WheelOdds {
  label: string;
  weight: number;
  total: number;
  percent: number;
}

export interface Wheel {
  name: string;
  title: string;
  choices: WheelChoice[];
  spin_seconds: number;
  version: number;
  created_at: string;
  updated_at: string;
  odds: WheelOdds[];
}

export interface WheelSettings {
  title: string;
  choices: WheelChoice[];
  spin_seconds?: number;
}

export interface WheelSpin {
  id: string;
  wheel: string;
  title: string;
  wheel_version: number;
  choices: WheelChoice[];
  total: number;
  draw: number;
  winner: number;
  label: string;
  started_by: string;
  spun_at: string;
  seconds: number;
  settled_at?: string;
  display?: '' | 'shown' | 'no_audience' | 'recovered';
}

const wid = (name: string) => encodeURIComponent(name);
export const getWheels = () => api<{ wheels: Wheel[]; spins: WheelSpin[]; pages: number }>('GET', '/v1/wheels');
export const addWheel = (name: string, s: WheelSettings) => api<Wheel>('POST', '/v1/wheels', { name, ...s });
export const setWheel = (name: string, s: WheelSettings) => api<Wheel>('PUT', `/v1/wheels/${wid(name)}`, s);
export const deleteWheel = (name: string) => api<{ deleted: boolean }>('DELETE', `/v1/wheels/${wid(name)}`);
export const spinWheel = (name: string, requestId: string) => api<WheelSpin>('POST', `/v1/wheels/${wid(name)}/spin`, { request_id: requestId });
export const getSpin = (id: string) => api<WheelSpin>('GET', `/v1/spins/${wid(id)}`);

// Viewers and points (docs/adr/0032-viewers-and-points.md).

export interface Viewer {
  id: string;
  platform: string;
  user_id: string;
  login: string;
  display_name: string;
  first_seen: string;
  last_seen: string;
  last_chat_at?: string;
  messages: number;
  forgotten_at?: string;
  balance: number;
}

export interface PointsTransaction {
  id: string;
  kind: 'credit' | 'debit' | 'adjust' | 'reverse';
  currency: string;
  viewer: string;
  amount: number;
  memo?: string;
  by: string;
  reverses?: string;
  reversed_by?: string;
  created_at: string;
}

export interface ViewerDetail extends Viewer {
  history: PointsTransaction[];
}

export interface PointsCurrency {
  id: string;
  name: string;
  allow_negative: boolean;
}

export interface PointsView {
  currency: PointsCurrency;
  summary: { transactions: number; viewers: number; issued: number };
  top: Viewer[];
}

const vid = (id: string) => encodeURIComponent(id);
export const getPoints = () => api<PointsView>('GET', '/v1/points');
export const getViewers = (q = '') => api<{ viewers: Viewer[] }>('GET', `/v1/viewers?q=${encodeURIComponent(q)}`);
export const getViewer = (id: string) => api<ViewerDetail>('GET', `/v1/viewers/${vid(id)}`);
export const exportViewer = (id: string) => api<unknown>('GET', `/v1/viewers/${vid(id)}/export`);
export const forgetViewer = (id: string) => api<Viewer>('POST', `/v1/viewers/${vid(id)}/forget`);
export const adjustPoints = (id: string, amount: number, reason: string, requestId: string) =>
  api<{ balance: number }>('POST', `/v1/viewers/${vid(id)}/points`, { amount, reason, request_id: requestId });
export const undoPoints = (txId: string, requestId: string) =>
  api<{ balance: number }>('POST', `/v1/points/transactions/${vid(txId)}/undo`, { request_id: requestId });
export const renamePoints = (name: string) => api<PointsCurrency>('PUT', '/v1/points/currency', { name });
export const auditPoints = () => api<{ ok: boolean; problems: string[] }>('GET', '/v1/points/audit');

/** How viewers earn points; an amount of 0 turns that rule off. */
export interface EarningRules {
  on: boolean;
  chat: { amount: number; every_seconds: number; live_only: boolean };
  time: { on: boolean; every_minutes: number; active: number; active_minutes: number; quiet: number };
  follow: number;
  sub: { tier1: number; tier2: number; tier3: number };
  gift: number;
  bits: number;
}
/** One time-in-chat payment: how many were paid, and why not, if not. */
export interface EarningTick {
  at: string;
  active: number;
  quiet: number;
  note?: string;
}
export interface EarningView {
  rules: EarningRules;
  live: 'live' | 'offline' | 'unknown';
  last_tick?: EarningTick;
}
/** A group to give to: everyone in chat, recent chatters, or subscribers. */
export interface GiveWho {
  audience: 'chat' | 'recent' | 'subs';
  minutes?: number;
}
export interface GiveGroup extends GiveWho {
  viewers: number;
  sample: string[];
  note?: string;
}
export interface PointsGive {
  id: string;
  audience: string;
  minutes?: number;
  words: string;
  amount: number;
  memo: string;
  by: string;
  viewers: number;
  total: number;
  created_at: string;
  paid_at?: string;
  undone_at?: string;
  applied: boolean;
}
export const previewGive = (who: GiveWho) => api<GiveGroup>('POST', '/v1/points/gives/preview', who);
export const giveAll = (who: GiveWho, amount: number, reason: string, requestId: string) =>
  api<PointsGive>('POST', '/v1/points/gives', { ...who, amount, reason, request_id: requestId });
export const getGives = () => api<{ gives: PointsGive[] }>('GET', '/v1/points/gives');
export const undoGive = (id: string) =>
  api<{ give: PointsGive; undone: number; short: string[] }>('POST', `/v1/points/gives/${encodeURIComponent(id)}/undo`);
/** Everything kept about every viewer, as streamgremlin.viewers-export/v1. */
export const exportAllViewers = () => api<unknown>('GET', '/v1/exports/viewers');

export const getEarning = () => api<EarningView>('GET', '/v1/points/earning');
export const setEarning = (rules: EarningRules) => api<EarningView>('PUT', '/v1/points/earning', rules);

// Packages: wheel looks installed from a ZIP (docs/adr/0036-packages-plugins-and-themes.md).

export interface Package {
  id: string;
  kind: 'theme' | 'lua';
  name: string;
  version: string;
  /** As the package states it: not verified. */
  publisher: string;
  description: string;
  license: string;
  homepage: string;
  digest: string;
  size: number;
  files: number;
  installed_at: string;
  updated_at: string;
  settings?: ThemeSetting[];
  /** Other versions kept, to roll back to, newest first. */
  kept: string[];
  /** The version Roll back returns to, if any. */
  rollback_to?: string;
  /** A Lua plugin's (docs/adr/0038-lua-plugins.md). */
  plugin?: PluginInfo;
}

export interface PluginParam {
  name: string;
  label: string;
  kind: 'text' | 'number' | 'switch' | 'choice';
  required?: boolean;
  default?: unknown;
  min?: number;
  max?: number;
  options?: string[];
  hint?: string;
}

export interface PluginAction {
  name: string;
  label: string;
  description: string;
  params?: PluginParam[];
}

export interface PluginInfo {
  /** Turned on; error says why it stopped, when it failed. */
  enabled: boolean;
  error?: string;
  /** What it asks for, and what each lets it do. */
  permissions: { name: string; what: string }[];
  actions: PluginAction[];
  /** Its settings' values, defaults filled in. */
  values: Record<string, unknown>;
  /** Seconds a call may take. */
  timeout: number;
  script: string;
  /** Templates it adds to the gallery, in its own section. */
  templates: { id: string; title: string }[];
}

/** One thing a plugin asked StreamGremlin to do. */
export interface PluginActivity {
  seq: number;
  at: string;
  version: string;
  /** chat.send, or log */
  call: string;
  outcome: 'ok' | 'refused' | 'failed';
  detail: string;
}

export interface InstalledPackage extends Package {
  /** The overlays whose look it is. */
  used_by: string[];
  /** The workflows using a plugin's steps. */
  workflows?: string[];
}

export interface PackagePreview extends Package {
  installed?: Package;
  /** Exactly this is installed already. */
  same: boolean;
  /** It has its own sounds. */
  sounds: boolean;
  /** Another version is installed: this replaces it (that one is kept). */
  update: boolean;
  /** This version is below the installed one. */
  older: boolean;
  /** The installed one says another publisher: installing needs this one's publisher typed. */
  publisher_change: boolean;
}

/** What an install, update or roll back did. */
export interface VersionAnswer extends Package {
  /** The version before, for an update or roll back. */
  from?: string;
  /** The overlays using it, and the settings each had reset to the new version's defaults. */
  overlays: { overlay: string; reset: string[] }[];
}

export interface PackageAuditEntry {
  seq: number;
  at: string;
  package_id: string;
  version: string;
  action: 'install' | 'update' | 'rollback' | 'remove' | 'refuse' | 'on' | 'off' | 'settings' | 'failed';
  detail: string;
}

/** Posts a file's bytes as they are (see addSound for why bytes). */
async function postBytes<T>(path: string, file: Blob): Promise<T> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let resp: Response;
  try {
    resp = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/zip' }, body: bytes });
  } catch {
    throw new ApiError(0, 'unreachable', "Couldn't reach the engine.");
  }
  const data = await resp.json().catch(() => undefined);
  if (!resp.ok) throw new ApiError(resp.status, data?.error?.code ?? 'unknown', data?.error?.message ?? resp.statusText);
  return data as T;
}

/** What trying a Lua script gave (docs/adr/0037-lua-scripts.md). */
export interface LuaTried {
  ok: boolean;
  result: unknown;
  log: string[];
  log_dropped?: number;
  error?: string;
  /** script, timeout, memory, output or crashed */
  kind?: string;
  took_ms: number;
}

/** Run a script once, as its step would, with a made-up test chat message. */
export const tryLua = (script: string, args?: Record<string, unknown>, timeLimit?: number) =>
  api<LuaTried>('POST', '/v1/lua/try', { script, ...(args ? { args } : {}), ...(timeLimit !== undefined ? { time_limit: timeLimit } : {}) });

export const getPackages = () => api<{ packages: InstalledPackage[] }>('GET', '/v1/packages');
/** What a ZIP is and what installing it would do; stores nothing. */
export const checkPackage = (file: Blob) => postBytes<PackagePreview>('/v1/packages/check', file);
/**
 * Install what was checked: digest is the one the streamer was shown;
 * publisher, the new publisher's name as typed, when it replaces another's.
 */
export const installPackage = (file: Blob, digest: string, publisher = '', permissions: string[] = []) =>
  postBytes<VersionAnswer>(
    `/v1/packages?digest=${encodeURIComponent(digest)}${publisher ? `&publisher=${encodeURIComponent(publisher)}` : ''}${
      permissions.length ? `&permissions=${encodeURIComponent(permissions.join(','))}` : ''
    }`,
    file,
  );
/** Turn a plugin on (loading it) or off. */
export const setPackageEnabled = (id: string, enabled: boolean) =>
  api<Package>('PUT', `/v1/packages/${encodeURIComponent(id)}/enabled`, { enabled });
/** Change a plugin's settings; one that's on starts again, fresh. */
export const setPackageSettings = (id: string, settings: Record<string, unknown>) =>
  api<Package>('PUT', `/v1/packages/${encodeURIComponent(id)}/settings`, { settings });
/** What a plugin has asked StreamGremlin to do, newest first. */
export const getPackageActivity = (id: string) =>
  api<{ activity: PluginActivity[] }>('GET', `/v1/packages/${encodeURIComponent(id)}/activity`);
/** Back to the version before (rollback_to). */
export const rollbackPackage = (id: string) =>
  api<VersionAnswer>('POST', `/v1/packages/${encodeURIComponent(id)}/rollback`);
export const removePackage = (id: string) =>
  api<{ package: Package; overlays: string[]; workflows?: string[] }>('DELETE', `/v1/packages/${encodeURIComponent(id)}`);
export const getPackageAudit = () => api<{ audit: PackageAuditEntry[] }>('GET', '/v1/packages/audit');
