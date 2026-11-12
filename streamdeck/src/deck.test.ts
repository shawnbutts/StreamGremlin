import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { WebSocketServer, type WebSocket } from 'ws';

// The built plugin, run as Stream Deck runs it, against a stand-in Stream
// Deck (Elgato's documented plugin protocol: register, willAppear, keyDown,
// sendToPlugin; setImage, setTitle, showOk, showAlert, global settings) and
// the real engine. `make verify` builds both first and sets
// STREAMGREMLIN_BIN. Never the user's data folder or a real engine on 7430.
const bin = process.env.STREAMGREMLIN_BIN;
// Stream Deck runs a plugin from its own folder; the SDK reads the manifest there.
const pluginDir = join(import.meta.dirname, '..', 'com.streamgremlin.streamdeck.sdPlugin');
const ACTION = 'com.streamgremlin.streamdeck.press';

type Msg = { event: string; context?: string; payload?: Record<string, unknown> };

describe.runIf(bin)('the plugin in a stand-in Stream Deck', () => {
  let dir = '';
  let home = '';
  let engine: ChildProcess | undefined;
  let deck: ChildProcess | undefined;
  let wss: WebSocketServer;
  let sock: WebSocket | undefined;
  const got: Msg[] = [];
  let globalSettings: Record<string, unknown> = {};
  const env = {
    ...Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('STREAMGREMLIN_'))),
    STREAMGREMLIN_DATA_DIR: '/nonexistent/do-not-use',
    STREAMGREMLIN_SECRET_STORE: 'file',
    STREAMGREMLIN_TWITCH_AUTH_URL: 'http://127.0.0.1:9',
  };
  const cli = (...args: string[]) => execFileSync(bin!, ['-data-dir', dir, ...args], { env, encoding: 'utf8' });
  const send = (m: Msg & { action?: string; device?: string }) => sock!.send(JSON.stringify(m));

  async function until<T>(what: string, f: () => T | undefined, ms = 8000): Promise<T> {
    const end = Date.now() + ms;
    for (;;) {
      const v = f();
      if (v) return v;
      if (Date.now() > end) throw new Error(`waited for ${what}; got ${JSON.stringify(got.slice(-8))}`);
      await new Promise((r) => setTimeout(r, 25));
    }
  }
  const last = (event: string, context = 'key1') => [...got].reverse().find((m) => m.event === event && m.context === context);
  const after = (n: number, event: string, context = 'key1') => got.slice(n).find((m) => m.event === event && m.context === context);

  beforeAll(async () => {
    dir = mkdtempSync(join(tmpdir(), 'sg-deck-'));
    home = mkdtempSync(join(tmpdir(), 'sg-home-'));
    engine = spawn(bin!, ['-data-dir', dir, 'engine', '-addr', '127.0.0.1:0', '-overlay-addr', '127.0.0.1:0'], { env, stdio: 'ignore' });
    await until('the engine', () => {
      try {
        return readFileSync(join(dir, 'api-addr'), 'utf8').trim();
      } catch {
        return undefined;
      }
    });
    cli('buttons', 'set', 'brb', '-label', 'Be right back', '-color', 'blue', '-icon', 'player-pause');

    wss = new WebSocketServer({ host: '127.0.0.1', port: 0 });
    wss.on('connection', (ws) => {
      sock = ws;
      ws.on('message', (raw) => {
        const m = JSON.parse(String(raw)) as Msg;
        got.push(m);
        if (m.event === 'getGlobalSettings') send({ event: 'didReceiveGlobalSettings', id: (m as { id?: string }).id, payload: { settings: globalSettings } } as Msg);
        if (m.event === 'setGlobalSettings') globalSettings = m.payload ?? {};
      });
    });
    await new Promise((r) => wss.on('listening', r));
    const port = (wss.address() as { port: number }).port;
    const info = {
      application: { font: '', language: 'en', platform: 'mac', platformVersion: '15.0', version: '7.1.0' },
      plugin: { uuid: 'com.streamgremlin.streamdeck', version: '0.1.0.0' },
      devicePixelRatio: 2,
      colors: {},
      devices: [{ id: 'dev1', name: 'Test deck', size: { columns: 5, rows: 3 }, type: 0 }],
    };
    deck = spawn(process.execPath, ['bin/plugin.js', '-port', String(port), '-pluginUUID', 'plug1', '-registerEvent', 'registerPlugin', '-info', JSON.stringify(info)], {
      cwd: pluginDir,
      env: { ...env, HOME: home, LOCALAPPDATA: home, STREAMGREMLIN_DECK_ADDRESS: readFileSync(join(dir, 'api-addr'), 'utf8').trim() },
      stdio: 'ignore',
    });
    await until('the plugin to register', () => got.find((m) => m.event === 'registerPlugin'));
  });

  afterAll(() => {
    deck?.kill();
    engine?.kill();
    wss?.close();
    rmSync(dir, { recursive: true, force: true });
    rmSync(home, { recursive: true, force: true });
  });

  it('asks to be paired, pairs from the panel, draws and presses the key, and notices being forgotten', async () => {
    const appear = { event: 'willAppear', action: ACTION, context: 'key1', device: 'dev1',
      payload: { settings: { buttonId: 'brb' }, coordinates: { column: 0, row: 0 }, controller: 'Keypad', isInMultiAction: false, state: 0 } };
    send(appear);
    await until('“Pair first”', () => (last('setTitle')?.payload?.title === 'Pair\nfirst' ? true : undefined));

    // The panel opens, and the code is typed.
    send({ event: 'propertyInspectorDidAppear', action: ACTION, context: 'key1', device: 'dev1' });
    const { code } = JSON.parse(cli('streamdeck', 'pair', '-wait=false', '-json')) as { code: string };
    let n = got.length;
    send({ event: 'sendToPlugin', action: ACTION, context: 'key1', payload: { type: 'pair', code: 'WRON-GGGG' } });
    const refused = await until('the refusal', () => after(n, 'sendToPropertyInspector'));
    expect(refused.payload).toMatchObject({ state: 'unpaired', problem: expect.stringContaining("That code isn't the one") });

    n = got.length;
    send({ event: 'sendToPlugin', action: ACTION, context: 'key1', payload: { type: 'pair', code } });
    const paired = await until('pairing', () => after(n, 'sendToPropertyInspector'));
    expect(paired.payload).toMatchObject({ state: 'ok', notice: expect.stringContaining('Paired'), buttons: [{ id: 'brb', label: 'Be right back' }] });
    expect(globalSettings.token).toBeTruthy(); // kept in global settings, never action settings
    expect(JSON.stringify(got.filter((m) => m.event === 'setSettings'))).not.toContain(String(globalSettings.token));
    expect(JSON.stringify(got.filter((m) => m.event === 'sendToPropertyInspector'))).not.toContain(String(globalSettings.token));
    await until('the key drawn', () => (decodeURIComponent(String(last('setImage')?.payload?.image)).includes('Be right') ? true : undefined));
    expect(decodeURIComponent(String(last('setImage')?.payload?.image))).toContain('fill="#3e8ed0"');
    expect(last('setTitle')?.payload?.title).toBe('');

    // A Nerd Fonts icon, shown alone, draws on the key as in the app.
    cli('buttons', 'set', 'brb', '-icon', 'nf-md-coffee', '-hide-label');
    await until('the Nerd Fonts icon', () => (/<path transform="[^"]*scale\([\d.]+ -[\d.]+\)[^"]*" fill="#111114" d="M/.test(decodeURIComponent(String(last('setImage')?.payload?.image))) ? true : undefined));

    // Pressing: nothing on starts from it, so the key says so.
    n = got.length;
    send({ event: 'keyDown', action: ACTION, context: 'key1', device: 'dev1', payload: { settings: { buttonId: 'brb' }, coordinates: { column: 0, row: 0 }, isInMultiAction: false, state: 0 } });
    await until('the press', () => after(n, 'showAlert') ?? after(n, 'showOk'));
    expect(after(n, 'showAlert')).toBeTruthy();
    expect(cli('events', '-json')).toContain('stream_deck');

    // Forgotten in StreamGremlin: the key says to pair again.
    const id = (JSON.parse(cli('streamdeck', '-json')) as { clients: { id: string }[] }).clients[0].id;
    cli('streamdeck', 'forget', id);
    await until('“Pair first” again', () => (last('setTitle')?.payload?.title === 'Pair\nfirst' ? true : undefined));

    // StreamGremlin stops: the key never looks working.
    engine?.kill();
    await until('“Not running”', () => (last('setTitle')?.payload?.title === 'Not\nrunning' ? true : undefined));
  }, 30_000);
});
