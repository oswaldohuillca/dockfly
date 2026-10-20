// Comandos de build habituales. Bun lee los package-lock.json y yarn.lock
// existentes, así que sirve también para proyectos creados con npm o yarn.

export interface Preset {
  id: string;
  label: string;
  install: string;
  build: string;
  output: string;
}

const bun = { install: 'bun install', build: 'bun run build' };

export const presets: Preset[] = [
  { id: 'vite', label: 'Vite (React, Vue, Svelte, Solid…)', ...bun, output: 'dist' },
  { id: 'astro', label: 'Astro', ...bun, output: 'dist' },
  { id: 'next', label: 'Next.js (output: "export")', ...bun, output: 'out' },
  { id: 'sveltekit', label: 'SvelteKit (adapter-static)', ...bun, output: 'build' },
  { id: 'nuxt', label: 'Nuxt (generate)', install: 'bun install', build: 'bun run generate', output: '.output/public' },
  { id: 'cra', label: 'Create React App', ...bun, output: 'build' },
  { id: 'docusaurus', label: 'Docusaurus', ...bun, output: 'build' },
  { id: 'html', label: 'HTML plano (sin build)', install: '', build: '', output: '' },
];

export const DEFAULT_IMAGE = 'oven/bun:1';

/** El preset que coincide con unos comandos, o undefined si son personalizados. */
export function matchPreset(install: string, build: string, output: string): Preset | undefined {
  return presets.find((p) => p.install === install && p.build === build && p.output === output);
}
