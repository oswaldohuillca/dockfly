import { createContext, useContext } from 'react';

import type { Status } from '@/lib/api';

// Estado de Traefik y Docker, que consulta el layout y usan algunas páginas.
export const StatusContext = createContext<Status | null>(null);

export const useStatus = () => useContext(StatusContext);
