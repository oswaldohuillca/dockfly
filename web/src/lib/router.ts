import { useEffect, useState } from 'react';

// Enrutado mínimo basado en el hash (#/sites/3). Basta para el panel y evita
// otra dependencia; además funciona igual sirviendo el SPA desde Go.

function current() {
  return window.location.hash.replace(/^#/, '') || '/';
}

export function useRoute(): string {
  const [route, setRoute] = useState(current);
  useEffect(() => {
    const onChange = () => setRoute(current());
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);
  return route;
}

export function navigate(path: string) {
  window.location.hash = path;
}
