import { useEffect, useState } from 'react';

import { api } from '@/lib/api';

/** Remote server names by id (empty when the user cannot see them). */
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
