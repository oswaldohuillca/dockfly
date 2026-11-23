// The .po catalogs are compiled by @lingui/vite-plugin when they are imported.
declare module '*.po' {
  import type { Messages } from '@lingui/core';
  export const messages: Messages;
}
