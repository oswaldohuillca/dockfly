import path from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import tailwindcss from '@tailwindcss/vite';
import { lingui } from '@lingui/vite-plugin';

export default defineConfig(({ command }) => ({
  // The router plugin goes before the React one: it generates src/routeTree.gen.ts
  // from src/routes and splits the code per route (options in tsr.config.json).
  // Lingui transforms its macros (t`…`, <Trans>) and compiles the .po catalogs
  // when they are imported. In a build, a missing translation is an error.
  plugins: [
    tanstackRouter(),
    lingui({ macroTransform: true, failOnMissing: command === 'build' }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // In development the Go backend runs separately (`make dev`).
    proxy: { '/api': 'http://127.0.0.1:8181' },
  },
}));
