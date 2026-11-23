import { defineConfig } from '@lingui/conf';

// The source text in the code is English (the default locale); the
// translations live in src/locales/<locale>/messages.po.
export default defineConfig({
  sourceLocale: 'en',
  locales: ['en', 'es'],
  catalogs: [{ path: '<rootDir>/src/locales/{locale}/messages', include: ['src'], exclude: ['**/routeTree.gen.ts'] }],
});
