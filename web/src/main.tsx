import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createRouter, RouterProvider } from '@tanstack/react-router';

import './index.css';
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

createRoot(document.getElementById('app')!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
