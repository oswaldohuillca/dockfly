import { t } from '@lingui/core/macro';
import { createContext, useContext } from 'react';

import type { Role, User } from '@/lib/api';

const rank: Record<Role, number> = { viewer: 1, deployer: 2, admin: 3 };

export function roleLabel(role: Role): string {
  switch (role) {
    case 'admin':
      return t`Administrator`;
    case 'deployer':
      return t`Deployer`;
    case 'viewer':
      return t`Read-only`;
  }
}

export function roleHint(role: Role): string {
  switch (role) {
    case 'admin':
      return t`Everything: proxy hosts, certificates, integrations, settings and users.`;
    case 'deployer':
      return t`Sites, apps, builds and deployments (including their environment variables).`;
    case 'viewer':
      return t`View the dashboard and logs, without changing anything or seeing secrets.`;
  }
}

/** Usuario de la sesión, para las páginas del panel. */
export const UserContext = createContext<User | null>(null);

export function can(user: User | null, role: Role): boolean {
  return !!user && rank[user.role] >= rank[role];
}

/** ¿Puede el usuario de la sesión hacer lo que requiere role? (La API lo vuelve a comprobar.) */
export function useCan(role: Role): boolean {
  return can(useContext(UserContext), role);
}
