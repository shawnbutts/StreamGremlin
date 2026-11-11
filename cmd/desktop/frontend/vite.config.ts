/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

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
  },
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  test: { environment: 'jsdom', include: ['src/**/*.test.ts'] },
});
