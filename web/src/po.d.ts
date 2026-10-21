// Los catálogos .po los compila @lingui/vite-plugin al importarlos.
declare module '*.po' {
  import type { Messages } from '@lingui/core';
  export const messages: Messages;
}
