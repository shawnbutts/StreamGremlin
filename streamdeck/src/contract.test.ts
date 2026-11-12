import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { Engine } from './engine';

// The plugin's side against the real engine (ADR-0042): `make verify`
// sets STREAMGREMLIN_BIN to the CLI it just built. Never the user's data
// folder or keyring (AGENTS.md).
const bin = process.env.STREAMGREMLIN_BIN;
const env = {
  ...Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('STREAMGREMLIN_'))),
  STREAMGREMLIN_DATA_DIR: '/nonexistent/do-not-use',
  STREAMGREMLIN_SECRET_STORE: 'file',
  STREAMGREMLIN_TWITCH_AUTH_URL: 'http://127.0.0.1:9',
};

describe.runIf(bin)('the plugin against the real engine', () => {
  let dir = '';
  let proc: ChildProcess | undefined;
  const cli = (...args: string[]) => execFileSync(bin!, ['-data-dir', dir, ...args], { env, encoding: 'utf8' });

  beforeAll(async () => {
    dir = mkdtempSync(join(tmpdir(), 'sg-deck-'));
    proc = spawn(bin!, ['-data-dir', dir, 'engine', '-addr', '127.0.0.1:0', '-overlay-addr', '127.0.0.1:0'], { env, stdio: 'ignore' });
    for (let i = 0; i < 300; i++) {
      try {
        readFileSync(join(dir, 'api-addr'), 'utf8');
        return;
      } catch {
        await new Promise((r) => setTimeout(r, 50));
      }
    }
    throw new Error('the engine didn’t start');
  });

  afterAll(() => {
    proc?.kill();
    rmSync(dir, { recursive: true, force: true });
  });

  it('finds it, pairs, reads and presses buttons, and notices being forgotten', async () => {
    const e = new Engine({ readFile: async () => readFileSync(join(dir, 'api-addr'), 'utf8'), defaultAddress: 'http://127.0.0.1:1', retryMs: 10 });
    const address = await e.find(undefined);
    expect(address).toMatch(/^http:\/\/127\.0\.0\.1:\d+$/);
    cli('buttons', 'set', 'brb', '-label', 'Be right back', '-color', 'blue', '-icon', 'player-pause');

    expect(await e.pair(address!, 'WRON-GGGG', 'Desk deck', 'r0')).toMatchObject({ ok: false, why: 'refused' });
    const { code } = JSON.parse(cli('streamdeck', 'pair', '-wait=false', '-json')) as { code: string };
    const paired = await e.pair(address!, code.toLowerCase(), 'Desk deck', 'r1');
    if (!paired.ok) throw new Error(paired.message);
    const token = paired.value.token;
    expect(await e.whoami(address!, token)).toEqual({ ok: true, value: { name: 'Desk deck' } });

    const list = await e.buttons(address!, token);
    expect(list.ok && list.value).toEqual([{ id: 'brb', label: 'Be right back', color: 'blue', icon: 'tabler:player-pause', hideLabel: false, starts: 0 }]);
    expect(await e.press(address!, token, 'brb')).toEqual({ ok: true, value: { started: 0 } });
    expect(await e.press(address!, token, 'nope')).toMatchObject({ ok: false, why: 'refused' });
    expect(cli('streamdeck')).toContain('Desk deck');

    cli('streamdeck', 'forget', paired.value.clientId);
    expect(await e.buttons(address!, token)).toMatchObject({ ok: false, why: 'unpaired' });

    proc?.kill();
    await new Promise((r) => setTimeout(r, 300));
    expect(await e.buttons(address!, token)).toMatchObject({ ok: false, why: 'unreachable' });
  });
});
