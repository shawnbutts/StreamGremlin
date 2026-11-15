/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';
import { packagesOf, writePackages } from '../../../packaging/sbom/bundled.mjs';

// Button icons (D-076): Tabler's outline pack, as one JSON file.
const tabler = fileURLToPath(new URL('./node_modules/@tabler/icons/tabler-nodes-outline.json', import.meta.url));
const alias = { 'tabler-icons-outline': tabler };

// The release SBOMs (D-117) list the npm packages in the built UI, which
// is embedded in the app, and the Nerd Fonts symbols kept in src/assets.
const sbomList = fileURLToPath(new URL('../../../bin/sbom/desktop-ui.json', import.meta.url));
const vendored = [
  { name: 'Nerd Fonts Symbols Only', version: '3.5.1', license: 'MIT', purl: 'pkg:github/ryanoasis/nerd-fonts@v3.5.1' },
];
function bundledPackages(): Plugin {
  return {
    name: 'streamgremlin-bundled-packages',
    apply: 'build',
    generateBundle(_options, bundle) {
      const ids: string[] = [];
      for (const out of Object.values(bundle)) {
        if (out.type === 'chunk') ids.push(...out.moduleIds);
      }
      writePackages(sbomList, packagesOf(ids), vendored);
    },
  };
}

export default defineConfig({
  plugins: [svelte(), bundledPackages()],
  // The brand kit at the repository root is the one source of colours and
  // icons. The overlay page and the wheel looks (and the example and
  // hostile test looks) are served by the engine; their tests run here.
  server: {
    fs: {
      allow: [
        '..',
        '../../../brand-kit',
        '../../../internal/overlays/page',
        '../../../internal/overlays/themes',
        '../../../examples/themes',
        '../../../tests/testdata/hostile-wheel',
      ],
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // Files only: the CSP allows nothing inline.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    // The button icon pack is one lazily loaded chunk of about 1.3 MB.
    chunkSizeWarningLimit: 1400,
  },
  resolve: process.env.VITEST ? { conditions: ['browser'], alias } : { alias },
  test: { environment: 'jsdom', include: ['src/**/*.test.ts'] },
});
