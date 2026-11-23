import { defineConfig } from 'astro/config'
import tailwindcss from '@tailwindcss/vite'

// Static output (the default): `bun run build` leaves the site in dist/.
export default defineConfig({
  site: 'https://dockfly.dev',
  // Same theme as the install block: the colours come from .astro-code in global.css.
  markdown: { shikiConfig: { theme: 'css-variables' } },
  vite: { plugins: [tailwindcss()] },
})
