// Writes the website's picture of Stream Deck keys (src/strip.ts) to the
// path given: node strip.mjs ../website/screenshots/streamdeck-keys.svg
import { build } from 'esbuild';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const out = process.argv[2];
if (!out) throw new Error('usage: node strip.mjs OUT.svg');
const dir = mkdtempSync(join(tmpdir(), 'sg-strip-'));
try {
  const file = join(dir, 'strip.mjs');
  await build({ entryPoints: ['src/strip.ts'], outfile: file, bundle: true, platform: 'node', format: 'esm', logLevel: 'warning' });
  const { strip } = await import(pathToFileURL(file).href);
  writeFileSync(out, strip());
  console.log(`   streamdeck-keys (${out})`);
} finally {
  rmSync(dir, { recursive: true, force: true });
}
