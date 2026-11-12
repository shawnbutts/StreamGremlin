/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';

// Button icons (D-076): Tabler's outline pack, as one JSON file.
const tabler = fileURLToPath(new URL('./node_modules/@tabler/icons/tabler-nodes-outline.json', import.meta.url));
const alias = { 'tabler-icons-outline': tabler };

export default defineConfig({
  plugins: [svelte()],
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
