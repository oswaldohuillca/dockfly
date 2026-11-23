import { createContext, useContext } from 'react';

import type { Status } from '@/lib/api';

// Traefik and Docker status, fetched by the layout and used by a few pages.
export const StatusContext = createContext<Status | null>(null);

export const useStatus = () => useContext(StatusContext);
