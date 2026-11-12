// Builds the plugin into com.streamgremlin.streamdeck.sdPlugin: the
// plugin's code (Node, one file) and the settings panel's script.
import { build } from 'esbuild';
import { mkdirSync, writeFileSync } from 'node:fs';

const out = 'com.streamgremlin.streamdeck.sdPlugin';
await build({
  entryPoints: ['src/plugin.ts'],
  outfile: `${out}/bin/plugin.js`,
  bundle: true,
  platform: 'node',
  format: 'esm',
  target: 'node20',
  legalComments: 'linked',
  // Bundled CommonJS (ws) requires Node's built-ins; ESM needs a require.
  banner: { js: "import { createRequire as __sgRequire } from 'node:module'; const require = __sgRequire(import.meta.url);" },
  logLevel: 'warning',
});
mkdirSync(`${out}/bin`, { recursive: true });
writeFileSync(`${out}/bin/package.json`, '{ "type": "module" }\n');
await build({
  entryPoints: ['src/ui/press.ts'],
  outfile: `${out}/ui/press.js`,
  bundle: true,
  platform: 'browser',
  format: 'iife',
  target: 'chrome130',
  logLevel: 'warning',
});
