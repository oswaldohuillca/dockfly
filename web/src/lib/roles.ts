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

/** The session's user, for the panel pages. */
export const UserContext = createContext<User | null>(null);

export function can(user: User | null, role: Role): boolean {
  return !!user && rank[user.role] >= rank[role];
}

/** Can the session's user do what role requires? (The API checks it again anyway.) */
export function useCan(role: Role): boolean {
  return can(useContext(UserContext), role);
}
