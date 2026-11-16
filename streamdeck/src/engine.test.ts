import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { afterEach, describe, expect, it } from 'vitest';
import { Engine, addressFile } from './engine';

// A stand-in engine: just enough of StreamGremlin's API (ADR-0042) to
// check the plugin's side. The contract test runs the real one.
type Handler = (req: IncomingMessage, body: string, res: ServerResponse) => void;
let servers: Server[] = [];
afterEach(() => {
  for (const s of servers) s.close();
  servers = [];
});

async function serve(h: Handler): Promise<string> {
  const s = createServer((req, res) => {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => h(req, body, res));
  });
  servers.push(s);
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r));
  const a = s.address();
  return `http://127.0.0.1:${typeof a === 'object' && a ? a.port : 0}`;
}

const json = (res: ServerResponse, status: number, body: unknown) => {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body));
};
const fail = (res: ServerResponse, status: number, message: string) => json(res, status, { error: { code: 'x', message } });

/** An address where nothing listens: a port that was free a moment ago. */
async function nothingThere(): Promise<string> {
  const s = createServer();
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r));
  const a = s.address();
  await new Promise((r) => s.close(r));
  return `http://127.0.0.1:${typeof a === 'object' && a ? a.port : 0}`;
}

const noFiles = async () => {
  throw new Error('no file');
};

describe('addressFile', () => {
  it('is in StreamGremlin’s data folder on each system', () => {
    expect(addressFile('darwin', {}, '/Users/ann')).toBe('/Users/ann/Library/Application Support/StreamGremlin/api-addr');
    expect(addressFile('win32', { LOCALAPPDATA: 'C:\\Users\\ann\\AppData\\Local' }, 'C:\\Users\\ann')).toBe(
      'C:\\Users\\ann\\AppData\\Local\\StreamGremlin\\api-addr',
    );
    expect(addressFile('win32', {}, 'C:\\Users\\ann')).toBeUndefined();
  });
});

describe('Engine', () => {
  it('finds StreamGremlin at the saved address, the address file, or not at all', async () => {
    const url = await serve((req, _b, res) => (req.url === '/v1/health' ? json(res, 200, { status: 'ok' }) : fail(res, 404, 'no')));
    const dead = await nothingThere();
    const e = new Engine({ readFile: noFiles, defaultAddress: dead });
    expect(await e.find(url)).toBe(url);
    expect(await e.find(undefined)).toBeUndefined(); // the default answers nothing
    const viaFile = new Engine({ readFile: async () => `${url}\n`, defaultAddress: dead });
    expect(await viaFile.find(dead)).toBe(url);
    // Only loopback addresses, whatever a file or field says.
    const elsewhere = new Engine({ readFile: async () => 'http://example.com:7430', defaultAddress: 'http://192.168.1.5:7430' });
    expect(await elsewhere.find('http://10.0.0.1:7430')).toBeUndefined();
  });

  it('pairs with the code, and says why it didn’t', async () => {
    const seen: unknown[] = [];
    const url = await serve((req, body, res) => {
      seen.push({ url: req.url, auth: req.headers.authorization, version: req.headers['streamgremlin-api-version'], body: JSON.parse(body || '{}') });
      const code = JSON.parse(body).code;
      if (code === 'K7QX-M3PA') return json(res, 201, { token: 'tok', client: { id: 'cl_1', name: 'Deck' }, permissions: [] });
      if (code === 'WRON-GGGG') return fail(res, 403, 'that code isn’t the one StreamGremlin shows; check it and try again');
      return fail(res, 409, 'StreamGremlin isn’t waiting for a code');
    });
    const e = new Engine({ readFile: noFiles });
    const ok = await e.pair(url, 'K7QX-M3PA', 'Deck', 'req1');
    expect(ok).toEqual({ ok: true, value: { token: 'tok', name: 'Deck', clientId: 'cl_1' } });
    expect(seen[0]).toMatchObject({ url: '/v1/pair', auth: undefined, version: '1', body: { code: 'K7QX-M3PA', name: 'Deck', request_id: 'req1' } });
    expect(await e.pair(url, 'WRON-GGGG', 'Deck', 'r')).toMatchObject({ ok: false, why: 'refused', message: expect.stringContaining('isn’t the one') });
    expect(await e.pair(url, 'OLDC-ODEE', 'Deck', 'r')).toMatchObject({ ok: false, why: 'refused' });
    expect(await e.pair(await nothingThere(), 'K7QX-M3PA', 'Deck', 'r')).toMatchObject({ ok: false, why: 'unreachable' });
  });

  it('reads buttons with its token, and knows when it’s been forgotten', async () => {
    let forgotten = false;
    const url = await serve((req, _b, res) => {
      if (forgotten || req.headers.authorization !== 'Bearer tok') return fail(res, 401, 'missing or wrong API token');
      json(res, 200, { buttons: [{ id: 'brb', label: 'Be right back', color: 'blue', icon: '', hide_label: false, starts: [{ id: 'w' }] }] });
    });
    const e = new Engine({ readFile: noFiles });
    const got = await e.buttons(url, 'tok');
    expect(got.ok && got.value[0]).toMatchObject({ id: 'brb', label: 'Be right back', starts: 1 });
    forgotten = true;
    expect(await e.buttons(url, 'tok')).toMatchObject({ ok: false, why: 'unpaired' });
    expect(await e.buttons(await nothingThere(), 'tok')).toMatchObject({ ok: false, why: 'unreachable' });
  });

  it('presses once: a lost answer is asked again with the same request id', async () => {
    const ids: string[] = [];
    let drop = 1;
    const url = await serve((req, body, res) => {
      ids.push(JSON.parse(body).request_id);
      if (drop-- > 0) return res.destroy(); // the answer never comes
      json(res, 200, { duplicate: ids.length > 1, started: [{ id: 'w' }], button: { label: 'Be right back' } });
    });
    const e = new Engine({ readFile: noFiles, retryMs: 10 });
    const r = await e.press(url, 'tok', 'brb');
    expect(r).toEqual({ ok: true, value: { started: 1 } });
    expect(ids).toHaveLength(2);
    expect(ids[0]).toBe(ids[1]); // the engine runs it once
    expect(ids[0]).toMatch(/^sd-[0-9a-f-]{36}$/);
  });

  it('says a press is unknown when no answer ever comes, and refused when the engine says no', async () => {
    let n = 0;
    const url = await serve((req, _b, res) => {
      n++;
      if (req.url === '/v1/buttons/gone/press') return fail(res, 404, 'no such button');
      res.destroy();
    });
    const e = new Engine({ readFile: noFiles, retryMs: 5 });
    expect(await e.press(url, 'tok', 'brb')).toMatchObject({ ok: false, why: 'unknown' });
    expect(n).toBe(3);
    expect(await e.press(url, 'tok', 'gone')).toMatchObject({ ok: false, why: 'refused', message: 'no such button' });
    // Not running at all: certainly not pressed, and not asked again.
    expect(await e.press(await nothingThere(), 'tok', 'brb')).toMatchObject({ ok: false, why: 'unreachable' });
  });

  it('counts once: a lost answer is asked again with the same request id', async () => {
    const seen: { url?: string; body: { change: string; request_id: string } }[] = [];
    let drop = 1;
    const url = await serve((req, body, res) => {
      seen.push({ url: req.url, body: JSON.parse(body) });
      if (drop-- > 0) return res.destroy();
      json(res, 200, { name: 'deaths', value: 8 });
    });
    const e = new Engine({ readFile: noFiles, retryMs: 10 });
    expect(await e.count(url, 'tok', 'deaths', 'add')).toEqual({ ok: true, value: { name: 'deaths', value: 8 } });
    expect(seen.map((s) => s.url)).toEqual(['/v1/deck/counters/deaths', '/v1/deck/counters/deaths']);
    expect(seen[0].body).toEqual(seen[1].body);
    expect(seen[0].body.change).toBe('add');
  });

  it('turns a switch by setting its state, chat lock with no id', async () => {
    const seen: { method?: string; url?: string; body: unknown }[] = [];
    const url = await serve((req, body, res) => {
      seen.push({ method: req.method, url: req.url, body: JSON.parse(body) });
      if (req.url?.includes('/timer/')) return fail(res, 403, 'that isn’t chosen for the Stream Deck');
      json(res, 200, { kind: 'chat_lock', label: 'Chat lock', on: true });
    });
    const e = new Engine({ readFile: noFiles, retryMs: 5 });
    expect(await e.turn(url, 'tok', 'chat_lock', '', true)).toMatchObject({ ok: true, value: { on: true } });
    expect(seen[0]).toEqual({ method: 'PUT', url: '/v1/deck/switches/chat_lock/-', body: { on: true } });
    expect(await e.turn(url, 'tok', 'timer', 'break', false)).toMatchObject({ ok: false, why: 'refused' });
  });
});
