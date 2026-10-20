import { createContext, useContext } from 'react';

import type { Role, User } from '@/lib/api';

const rank: Record<Role, number> = { viewer: 1, deployer: 2, admin: 3 };

export const roleLabel: Record<Role, string> = {
  admin: 'Administrador',
  deployer: 'Despliegues',
  viewer: 'Solo lectura',
};

export const roleHint: Record<Role, string> = {
  admin: 'Todo: proxy hosts, certificados, integraciones, ajustes y usuarios.',
  deployer: 'Sitios, apps, builds y despliegues (incluidas sus variables de entorno).',
  viewer: 'Ver el panel y los logs, sin cambiar nada ni ver secretos.',
};

/** Usuario de la sesión, para las páginas del panel. */
export const UserContext = createContext<User | null>(null);

export function can(user: User | null, role: Role): boolean {
  return !!user && rank[user.role] >= rank[role];
}

/** ¿Puede el usuario de la sesión hacer lo que requiere role? (La API lo vuelve a comprobar.) */
export function useCan(role: Role): boolean {
  return can(useContext(UserContext), role);
}
