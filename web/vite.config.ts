import path from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { tanstackRouter } from '@tanstack/router-plugin/vite';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  // El plugin del router va antes que el de React: genera src/routeTree.gen.ts
  // a partir de src/routes y divide el código por ruta (opciones en tsr.config.json).
  plugins: [tanstackRouter(), react(), tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // En desarrollo, el backend Go corre aparte (`make dev`).
    proxy: { '/api': 'http://127.0.0.1:8181' },
  },
});
