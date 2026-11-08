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
}

export interface ChatStatus {
  state: 'off' | 'connecting' | 'connected' | 'disconnected';
  detail?: string;
  since: string;
  bot_moderator?: boolean;
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
  enabled: boolean;
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
  kind: 'command' | 'who' | 'text' | 'scene' | 'source' | 'on_off';
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
}

export interface Overview {
  checklist: SetupStep[];
  set_up: boolean;
  attention: AttentionItem[];
  workflows: { total: number; on: number; tested: boolean; answered: boolean; failed_24h: number };
}

export const getOverview = () => api<Overview>('GET', '/v1/overview');

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
