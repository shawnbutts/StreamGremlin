// The key's settings panel (Stream Deck's property inspector). It talks
// only to the plugin, over Stream Deck's own connection; the plugin talks
// to StreamGremlin. Stream Deck calls connectElgatoStreamDeckSocket.
import { codeProblem, statusWords, type Status, type ToPlugin } from '../messages';

type Settings = {
  buttonId?: string;
  name?: string;
  change?: string;
  what?: string;
  switch?: string;
  run?: string;
  scene?: string;
  countdown?: string;
  press?: string;
};
const KINDS = ['press', 'counter', 'status', 'switch', 'run', 'scene', 'countdown', 'ad'] as const;
type Kind = (typeof KINDS)[number];

let socket: WebSocket | undefined;
let context = '';
let actionUUID = '';
let settings: Settings = {};
let kind: Kind = 'press';

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

function send(event: string, extra: Record<string, unknown>): void {
  socket?.send(JSON.stringify({ event, context, ...extra }));
}

function toPlugin(payload: ToPlugin): void {
  send('sendToPlugin', { action: actionUUID, payload });
}

function render(s: Status): void {
  $('status').textContent = statusWords(s);
  $('status').dataset.state = s.state;
  $('pairing').hidden = s.state === 'ok';
  $('paired').hidden = s.state !== 'ok';
  $('problem').textContent = s.problem ?? '';
  $('notice').textContent = s.notice ?? '';
  if (s.address && !$<HTMLInputElement>('address').value) $<HTMLInputElement>('address').placeholder = s.address;
  for (const k of KINDS) $(`for-${k}`).hidden = k !== kind;
  $('old').hidden = s.deck || kind === 'press';
  const ok = s.state === 'ok';
  if (kind === 'press') {
    fill($('button'), s.buttons.map((b) => [b.id, b.label]), settings.buttonId, ok,
      s.buttons.length ? 'Pick a button' : 'No buttons yet: make them on Live', 'That button was deleted');
  } else if (kind === 'counter') {
    fill($('counter'), s.counters.map((c) => [c, c]), settings.name, ok,
      s.counters.length ? 'Pick a counter' : 'None chosen in StreamGremlin yet', 'No longer chosen in StreamGremlin');
    $<HTMLSelectElement>('change').value = settings.change ?? 'add';
  } else if (kind === 'status') {
    $<HTMLSelectElement>('what').value = settings.what ?? 'live';
  } else if (kind === 'switch') {
    fill($('switch'), s.switches.map((w) => [w.key, w.label]), settings.switch, ok,
      s.switches.length ? 'Pick a switch' : 'None chosen in StreamGremlin yet', 'No longer chosen in StreamGremlin');
  } else if (kind === 'run') {
    fill($('run'), s.runs.map((r) => [r.id, r.label]), settings.run, ok,
      s.runs.length ? 'Pick a workflow' : 'None chosen in StreamGremlin yet', 'No longer chosen in StreamGremlin');
  } else if (kind === 'scene') {
    fill($('scene'), s.scenes.map((n) => [n, n]), settings.scene, ok,
      s.scenes.length ? 'Pick a scene' : 'Not allowed in StreamGremlin, or OBS isn’t connected', 'Not in OBS now');
  } else if (kind === 'countdown') {
    fill($('countdown'), s.countdowns.map((c) => [c.name, c.label]), settings.countdown, ok,
      s.countdowns.length ? 'Pick one' : 'None chosen in StreamGremlin yet', 'No longer chosen in StreamGremlin');
    $<HTMLSelectElement>('press').value = settings.press ?? 'toggle';
  }
}

/** Fills a picker, keeping a choice that's gone as gone, never silently changed. */
function fill(pick: HTMLSelectElement, items: [string, string][], current: string | undefined, ok: boolean, empty: string, gone: string): void {
  pick.replaceChildren();
  pick.add(new Option(empty, ''));
  for (const [value, label] of items) pick.add(new Option(label, value));
  if (current && !items.some(([value]) => value === current)) pick.add(new Option(ok ? gone : 'Unknown until StreamGremlin answers', current));
  pick.value = current ?? '';
  pick.disabled = !ok;
}

/** Saves one setting of this key. */
function set(key: keyof Settings, value: string): void {
  settings = { ...settings, [key]: value || undefined };
  send('setSettings', { payload: settings });
}

function wire(): void {
  $('pair').addEventListener('click', () => {
    const code = $<HTMLInputElement>('code').value;
    const bad = codeProblem(code);
    $('problem').textContent = bad ?? '';
    $('notice').textContent = bad ? '' : 'Pairing…';
    if (!bad) toPlugin({ type: 'pair', code, address: $<HTMLInputElement>('address').value.trim() || undefined });
  });
  $('unpair').addEventListener('click', () => toPlugin({ type: 'unpair' }));
  const on = (id: string, key: keyof Settings) =>
    $<HTMLSelectElement>(id).addEventListener('change', (e) => set(key, (e.target as HTMLSelectElement).value));
  on('button', 'buttonId');
  on('counter', 'name');
  on('change', 'change');
  on('what', 'what');
  on('switch', 'switch');
  on('run', 'run');
  on('scene', 'scene');
  on('countdown', 'countdown');
  on('press', 'press');
}

(window as unknown as Record<string, unknown>).connectElgatoStreamDeckSocket = (
  port: string,
  uuid: string,
  registerEvent: string,
  _info: string,
  actionInfo: string,
) => {
  const ai = JSON.parse(actionInfo) as { action: string; payload?: { settings?: Settings } };
  context = uuid;
  actionUUID = ai.action;
  const k = ai.action.split('.').pop();
  kind = (KINDS as readonly string[]).includes(k ?? '') ? (k as Kind) : 'press';
  settings = ai.payload?.settings ?? {};
  socket = new WebSocket(`ws://127.0.0.1:${port}`);
  socket.addEventListener('open', () => {
    socket?.send(JSON.stringify({ event: registerEvent, uuid }));
    toPlugin({ type: 'hello' });
  });
  socket.addEventListener('message', (m) => {
    const d = JSON.parse(String(m.data)) as { event: string; payload?: unknown };
    if (d.event === 'sendToPropertyInspector') render(d.payload as Status);
    if (d.event === 'didReceiveSettings') settings = ((d.payload as { settings?: Settings })?.settings ?? {}) as Settings;
  });
};

wire();
