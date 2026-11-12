import { defineConfig } from 'astro/config'
import tailwindcss from '@tailwindcss/vite'

// Salida estática (por defecto): `bun run build` deja el sitio en dist/.
export default defineConfig({
  site: 'https://dockfly.dev',
  // Mismo tema que el bloque de instalación: los colores salen de .astro-code en global.css.
  markdown: { shikiConfig: { theme: 'css-variables' } },
  vite: { plugins: [tailwindcss()] },
})
