// The usual build commands. Bun reads existing package-lock.json and
// yarn.lock files, so it also works for projects created with npm or yarn.

import type { MessageDescriptor } from '@lingui/core';
import { msg } from '@lingui/core/macro';

export interface Preset {
  id: string;
  /** The framework's name, or a translatable message when it is not a proper name. */
  label: string | MessageDescriptor;
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
  { id: 'html', label: msg`Plain HTML (no build)`, install: '', build: '', output: '' },
];

export const DEFAULT_IMAGE = 'oven/bun:1';

/** The preset matching a given set of commands, or undefined when they are custom. */
export function matchPreset(install: string, build: string, output: string): Preset | undefined {
  return presets.find((p) => p.install === install && p.build === build && p.output === output);
}
