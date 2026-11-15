// Builds the plugin into com.streamgremlin.streamdeck.sdPlugin: the
// plugin's code (Node, one file) and the settings panel's script.
import { build } from 'esbuild';
import { mkdirSync, writeFileSync } from 'node:fs';
import { packagesOf, writePackages } from '../packaging/sbom/bundled.mjs';

const out = 'com.streamgremlin.streamdeck.sdPlugin';
const plugin = await build({
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
  metafile: true,
});
mkdirSync(`${out}/bin`, { recursive: true });
writeFileSync(`${out}/bin/package.json`, '{ "type": "module" }\n');
const ui = await build({
  entryPoints: ['src/ui/press.ts'],
  outfile: `${out}/ui/press.js`,
  bundle: true,
  platform: 'browser',
  format: 'iife',
  target: 'chrome130',
  logLevel: 'warning',
  metafile: true,
});

// The release SBOMs (D-117) list the npm packages bundled into both, and
// the Nerd Fonts outlines the plugin ships beside them (src/nerd.ts).
writePackages('../bin/sbom/streamdeck.json', packagesOf([...Object.keys(plugin.metafile.inputs), ...Object.keys(ui.metafile.inputs)]), [
  { name: 'Nerd Fonts Symbols Only', version: '3.5.1', license: 'MIT', purl: 'pkg:github/ryanoasis/nerd-fonts@v3.5.1' },
]);
