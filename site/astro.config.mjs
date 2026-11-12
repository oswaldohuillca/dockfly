import { defineConfig } from 'astro/config'
import tailwindcss from '@tailwindcss/vite'

// Salida estática (por defecto): `npm run build` deja el sitio en dist/.
export default defineConfig({
  site: 'https://dockfly.dev',
  vite: { plugins: [tailwindcss()] },
})
