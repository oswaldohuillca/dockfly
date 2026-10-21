import { defineConfig } from '@lingui/conf';

// El texto fuente del código está en inglés (el idioma por defecto); las
// traducciones viven en src/locales/<idioma>/messages.po.
export default defineConfig({
  sourceLocale: 'en',
  locales: ['en', 'es'],
  catalogs: [{ path: '<rootDir>/src/locales/{locale}/messages', include: ['src'], exclude: ['**/routeTree.gen.ts'] }],
});
