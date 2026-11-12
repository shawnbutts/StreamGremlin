// The key's settings panel (Stream Deck's property inspector). It talks
// only to the plugin, over Stream Deck's own connection; the plugin talks
// to StreamGremlin. Stream Deck calls connectElgatoStreamDeckSocket.
import { codeProblem, statusWords, type Status, type ToPlugin } from '../messages';

type Settings = { buttonId?: string };

let socket: WebSocket | undefined;
let context = '';
let actionUUID = '';
let settings: Settings = {};

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
  const pick = $<HTMLSelectElement>('button');
  pick.replaceChildren();
  const none = new Option(s.buttons.length ? 'Pick a button' : 'No buttons yet: make them on Live', '');
  pick.add(none);
  for (const b of s.buttons) pick.add(new Option(b.label, b.id));
  if (settings.buttonId && !s.buttons.some((b) => b.id === settings.buttonId)) {
    pick.add(new Option(s.state === 'ok' ? 'That button was deleted' : 'Unknown until StreamGremlin answers', settings.buttonId));
  }
  pick.value = settings.buttonId ?? '';
  pick.disabled = s.state !== 'ok';
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
  $<HTMLSelectElement>('button').addEventListener('change', (e) => {
    settings = { ...settings, buttonId: (e.target as HTMLSelectElement).value || undefined };
    send('setSettings', { payload: settings });
  });
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
