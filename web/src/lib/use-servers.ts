import { useEffect, useState } from 'react';

import { api } from '@/lib/api';

/** Nombres de los servidores remotos por id (vacío si el usuario no puede verlos). */
export function useServerNames(): Record<number, string> {
  const [names, setNames] = useState<Record<number, string>>({});
  useEffect(() => {
    api
      .listServers()
      .then((list) => setNames(Object.fromEntries(list.map((s) => [s.id, s.name]))))
      .catch(() => {});
  }, []);
  return names;
}
