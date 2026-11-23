import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createRouter, RouterProvider } from '@tanstack/react-router';
import { i18n } from '@lingui/core';
import { I18nProvider } from '@lingui/react';

import './index.css';
import { activateLocale, savedLocale } from './lib/i18n';
import { routeTree } from './routeTree.gen';

const router = createRouter({
  routeTree,
  // Preloads a page's code when the pointer hovers over its link.
  defaultPreload: 'intent',
  scrollRestoration: true,
});

// Registers the router so that Link, navigate and the route params are typed.
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

// The language is loaded before anything is rendered, so there is no flash of English.
activateLocale(savedLocale()).then(() =>
  createRoot(document.getElementById('app')!).render(
    <StrictMode>
      <I18nProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nProvider>
    </StrictMode>,
  ),
);
