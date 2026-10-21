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
  // Precarga el código de una página al pasar el ratón por su enlace.
  defaultPreload: 'intent',
  scrollRestoration: true,
});

// Registra el router para que Link, navigate y los parámetros estén tipados.
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

// El idioma se carga antes de pintar nada: así no hay un instante en inglés.
activateLocale(savedLocale()).then(() =>
  createRoot(document.getElementById('app')!).render(
    <StrictMode>
      <I18nProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nProvider>
    </StrictMode>,
  ),
);
