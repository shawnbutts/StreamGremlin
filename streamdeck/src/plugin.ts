// StreamGremlin's Stream Deck plugin (M7 slice 5; ADR-0042). One action:
// a key that presses a StreamGremlin button. The plugin pairs once with
// the code StreamGremlin shows, keeps its own token in the plugin's global
// settings, reads the buttons every few seconds while keys are showing,
// and draws each key from them, or says why it can't.
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
import { Engine, addressFile, loopback, type DeckButton } from './engine';
import { keyView, type DeckState } from './keys';
import { codeProblem, type Status, type ToPlugin } from './messages';

type JsonValue = Parameters<typeof streamDeck.ui.sendToPropertyInspector>[0];
type Global = { address?: string; token?: string; name?: string };
type KeySettings = { buttonId?: string };

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
let problem = '';
const keys = new Map<string, { action: Action<KeySettings>; buttonId?: string; drawn?: string }>();
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
    } else {
      state = r.why === 'unpaired' ? 'unpaired' : 'unreachable';
    }
  }
  await draw();
}

async function draw(): Promise<void> {
  for (const k of keys.values()) {
    if (!k.action.isKey()) continue;
    const v = keyView({ state, buttonId: k.buttonId, buttons });
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

@action({ UUID: 'com.streamgremlin.streamdeck.press' })
class PressButton extends SingletonAction<KeySettings> {
  override async onWillAppear(ev: WillAppearEvent<KeySettings>): Promise<void> {
    keys.set(ev.action.id, { action: ev.action, buttonId: ev.payload.settings.buttonId });
    watch();
    await refresh();
  }

  override onWillDisappear(ev: WillDisappearEvent<KeySettings>): void {
    keys.delete(ev.action.id);
    watch();
  }

  override async onDidReceiveSettings(ev: DidReceiveSettingsEvent<KeySettings>): Promise<void> {
    const k = keys.get(ev.action.id);
    if (k) k.buttonId = ev.payload.settings.buttonId;
    await draw();
  }

  override async onKeyDown(ev: KeyDownEvent<KeySettings>): Promise<void> {
    const id = ev.payload.settings.buttonId;
    if (!id) return ev.action.showAlert();
    if (state !== 'ok' || !address || !global.token) await refresh();
    if (state !== 'ok' || !address || !global.token) return ev.action.showAlert();
    const r = await engine.press(address, global.token, id);
    if (r.ok && r.value.started > 0) return ev.action.showOk();
    // Pressed but nothing on starts from it, refused, or unknown: say so.
    if (!r.ok) log.warn(`Press of ${id}: ${r.why}: ${r.message}`);
    if (!r.ok && r.why === 'unpaired') {
      state = 'unpaired';
      await draw();
    }
    return ev.action.showAlert();
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

streamDeck.actions.registerAction(new PressButton());
streamDeck.settings.onDidReceiveGlobalSettings<Global>((ev) => {
  global = ev.settings;
});
await streamDeck.connect();
global = await streamDeck.settings.getGlobalSettings<Global>();
await refresh();
