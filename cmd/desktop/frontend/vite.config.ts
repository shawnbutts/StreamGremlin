/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  // The brand kit at the repository root is the one source of colours and
  // icons. The overlay page is served by the engine; its tests run here.
  server: { fs: { allow: ['..', '../../../brand-kit', '../../../internal/overlays/page'] } },
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
