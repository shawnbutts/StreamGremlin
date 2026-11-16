// StreamGremlin's Stream Deck plugin (M7 slice 5, ADR-0042; M10 slice 1,
// D-123, ADR-0053). Four actions: a key that presses a StreamGremlin
// button, and counter, status and switch keys for what the streamer chose
// for the deck. The plugin pairs once with the code StreamGremlin shows,
// keeps its own token in the plugin's global settings, reads StreamGremlin
// every few seconds while keys are showing (and at once after a press),
// and draws each key from it, or says why it can't.
import streamDeck, {
  action,
  SingletonAction,
  type Action,
  type DidReceiveSettingsEvent,
  type KeyDownEvent,
  type SendToPluginEvent,
  type WillAppearEvent,
  type WillDisappearEvent,
} from '@elgato/streamdeck';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { homedir, hostname } from 'node:os';
import { Engine, addressFile, loopback, switchKey, type Change, type DeckButton, type DeckView, type Result, type SwitchKind } from './engine';
import { counterView, keyView, statusView, switchView, type DeckState, type KeyView, type StatusWhat } from './keys';
import { codeProblem, type Status, type ToPlugin } from './messages';
import { loadNerd, type NerdLookup } from './nerd';

type JsonValue = Parameters<typeof streamDeck.ui.sendToPropertyInspector>[0];
type Global = { address?: string; token?: string; name?: string };
/** Every action's settings; each uses its own. */
type KeySettings = { buttonId?: string; name?: string; change?: Change; what?: StatusWhat; switch?: string };
type Kind = 'press' | 'counter' | 'status' | 'switch';

const REFRESH_MS = 3000;

const file = addressFile(process.platform, process.env, homedir());
// Tests point the plugin at their own engine, never one on 7430.
const engine = new Engine({
  readFile: () => (file ? readFile(file, 'utf8') : Promise.reject(new Error('no address file'))),
  defaultAddress: loopback(process.env.STREAMGREMLIN_DECK_ADDRESS),
});
const log = streamDeck.logger;

let global: Global = {};
let address: string | undefined;
let state: DeckState = 'unreachable';
let buttons: DeckButton[] = [];
// Undefined until read, or when StreamGremlin is too old to say.
let deck: DeckView | undefined;
let problem = '';
// Nerd Fonts outlines, loaded when a button first uses one (D-081).
let nerd: NerdLookup | undefined;
const keys = new Map<string, { kind: Kind; action: Action<KeySettings>; settings: KeySettings; drawn?: string }>();
let timer: ReturnType<typeof setInterval> | undefined;

/** Reads StreamGremlin again and redraws the keys. */
async function refresh(): Promise<void> {
  address = await engine.find(global.address ?? address);
  if (!address) {
    state = 'unreachable';
  } else if (!global.token) {
    state = 'unpaired';
  } else {
    const r = await engine.buttons(address, global.token);
    if (r.ok) {
      buttons = r.value;
      state = 'ok';
      if (!nerd && buttons.some((b) => b.icon.startsWith('nf:'))) nerd = await loadNerd('icons/nerd-paths.json.gz');
      const d = await engine.deck(address, global.token);
      deck = d.ok ? d.value : undefined;
      if (!d.ok && d.why !== 'refused') state = d.why === 'unpaired' ? 'unpaired' : 'unreachable';
    } else {
      state = r.why === 'unpaired' ? 'unpaired' : 'unreachable';
    }
  }
  await draw();
}

function view(k: { kind: Kind; settings: KeySettings }): KeyView {
  const s = k.settings;
  switch (k.kind) {
    case 'press':
      return keyView({ state, buttonId: s.buttonId, buttons }, nerd);
    case 'counter':
      return counterView({ state, name: s.name, change: s.change, deck });
    case 'status':
      return statusView({ state, what: s.what, deck });
    case 'switch':
      return switchView({ state, sw: s.switch, deck });
  }
}

async function draw(): Promise<void> {
  for (const k of keys.values()) {
    if (!k.action.isKey()) continue;
    const v = view(k);
    const sig = v.title + v.image;
    if (k.drawn === sig) continue; // Elgato asks for few updates
    k.drawn = sig;
    await k.action.setImage(v.image);
    await k.action.setTitle(v.title);
  }
}

function watch(): void {
  if (keys.size > 0 && !timer) timer = setInterval(() => void refresh(), REFRESH_MS);
  if (keys.size === 0 && timer) {
    clearInterval(timer);
    timer = undefined;
  }
}

function status(extra: Partial<Status> = {}): Status {
  return {
    type: 'status',
    state,
    name: global.name,
    address,
    buttons: buttons.map((b) => ({ id: b.id, label: b.label })),
    counters: deck?.counters.map((c) => c.name) ?? [],
    switches: deck?.switches.filter((s) => !s.gone).map((s) => ({ key: switchKey(s.kind, s.id), label: s.label })) ?? [],
    deck: state !== 'ok' || !!deck,
    ...(problem ? { problem } : {}),
    ...extra,
  };
}

async function pair(code: string, typedAddress: string | undefined): Promise<Partial<Status>> {
  const bad = codeProblem(code);
  if (bad) return { problem: bad };
  const where = await engine.find(loopback(typedAddress) ?? global.address);
  if (!where) return { problem: 'StreamGremlin isn’t answering. Start it, or type the address its Connections screen shows.' };
  const r = await engine.pair(where, code, `Stream Deck on ${hostname()}`.slice(0, 60), `pair-${randomUUID()}`);
  if (!r.ok) return { problem: r.message.charAt(0).toUpperCase() + r.message.slice(1) };
  global = { address: where, token: r.value.token, name: r.value.name };
  await streamDeck.settings.setGlobalSettings(global);
  log.info(`Paired as ${r.value.name} (${r.value.clientId}).`); // never the token
  return { notice: `Paired as “${r.value.name}”.` };
}

/** Ready to send: StreamGremlin answers and this deck is paired. */
async function ready(): Promise<{ address: string; token: string } | undefined> {
  if (state !== 'ok' || !address || !global.token) await refresh();
  if (state !== 'ok' || !address || !global.token) return undefined;
  return { address, token: global.token };
}

/** After a press: show how it went, and redraw from StreamGremlin at once. */
async function after<T>(ev: KeyDownEvent<KeySettings>, what: string, r: Result<T>, worked: (v: T) => boolean): Promise<void> {
  if (!r.ok) {
    log.warn(`${what}: ${r.why}: ${r.message}`);
    if (r.why === 'unpaired') state = 'unpaired';
  }
  await refresh();
  return r.ok && worked(r.value) ? ev.action.showOk() : ev.action.showAlert();
}

/** What every key shares: being drawn, its settings, and the settings panel. */
class Key extends SingletonAction<KeySettings> {
  constructor(private readonly kind: Kind) {
    super();
  }

  override async onWillAppear(ev: WillAppearEvent<KeySettings>): Promise<void> {
    keys.set(ev.action.id, { kind: this.kind, action: ev.action, settings: ev.payload.settings });
    watch();
    await refresh();
  }

  override onWillDisappear(ev: WillDisappearEvent<KeySettings>): void {
    keys.delete(ev.action.id);
    watch();
  }

  override async onDidReceiveSettings(ev: DidReceiveSettingsEvent<KeySettings>): Promise<void> {
    const k = keys.get(ev.action.id);
    if (k) k.settings = ev.payload.settings;
    await draw();
  }

  override async onSendToPlugin(ev: SendToPluginEvent<JsonValue, KeySettings>): Promise<void> {
    const msg = ev.payload as ToPlugin;
    let extra: Partial<Status> = {};
    problem = '';
    if (msg?.type === 'pair') {
      extra = await pair(String(msg.code ?? ''), typeof msg.address === 'string' ? msg.address : undefined);
    } else if (msg?.type === 'unpair') {
      global = { address: global.address };
      await streamDeck.settings.setGlobalSettings(global);
      extra = { notice: 'Unpaired here. To stop StreamGremlin knowing this deck, Forget it on its Connections screen.' };
    }
    await refresh();
    await streamDeck.ui.sendToPropertyInspector(status(extra) as unknown as JsonValue);
  }
}

@action({ UUID: 'com.streamgremlin.streamdeck.press' })
class PressButton extends Key {
  constructor() {
    super('press');
  }

  override async onKeyDown(ev: KeyDownEvent<KeySettings>): Promise<void> {
    const id = ev.payload.settings.buttonId;
    if (!id) return ev.action.showAlert();
    const at = await ready();
    if (!at) return ev.action.showAlert();
    // Pressed but nothing on starts from it, refused, or unknown: say so.
    await after(ev, `Press of ${id}`, await engine.press(at.address, at.token, id), (v) => v.started > 0);
  }
}

@action({ UUID: 'com.streamgremlin.streamdeck.counter' })
class CounterKey extends Key {
  constructor() {
    super('counter');
  }

  override async onKeyDown(ev: KeyDownEvent<KeySettings>): Promise<void> {
    const { name, change = 'add' } = ev.payload.settings;
    if (!name) return ev.action.showAlert();
    const at = await ready();
    if (!at) return ev.action.showAlert();
    await after(ev, `Counter ${name} ${change}`, await engine.count(at.address, at.token, name, change), () => true);
  }
}

@action({ UUID: 'com.streamgremlin.streamdeck.status' })
class StatusKey extends Key {
  constructor() {
    super('status');
  }

  // A status key only shows; a press reads StreamGremlin again.
  override async onKeyDown(): Promise<void> {
    await refresh();
  }
}

@action({ UUID: 'com.streamgremlin.streamdeck.switch' })
class SwitchKey extends Key {
  constructor() {
    super('switch');
  }

  override async onKeyDown(ev: KeyDownEvent<KeySettings>): Promise<void> {
    const sw = ev.payload.settings.switch;
    if (!sw) return ev.action.showAlert();
    const at = await ready();
    const now = deck?.switches.find((s) => switchKey(s.kind, s.id) === sw);
    // Unknown, gone or not chosen: a press can't know which way to turn it.
    if (!at || !now || now.gone || now.on === null) return ev.action.showAlert();
    const [kind, id] = sw.split(/:(.*)/s) as [SwitchKind, string];
    await after(ev, `Switch ${sw}`, await engine.turn(at.address, at.token, kind, id, !now.on), (v) => v.on === !now.on);
  }
}

streamDeck.actions.registerAction(new PressButton());
streamDeck.actions.registerAction(new CounterKey());
streamDeck.actions.registerAction(new StatusKey());
streamDeck.actions.registerAction(new SwitchKey());
streamDeck.settings.onDidReceiveGlobalSettings<Global>((ev) => {
  global = ev.settings;
});
await streamDeck.connect();
global = await streamDeck.settings.getGlobalSettings<Global>();
await refresh();
