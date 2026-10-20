import { api, type User } from '@/lib/api';

// Usuario de la sesión, comprobado una sola vez y reutilizado entre
// navegaciones. undefined = todavía no se ha comprobado.
let current: User | null | undefined;

export async function getUser(): Promise<User | null> {
  if (current === undefined) {
    current = await api.me().catch(() => null);
  }
  return current;
}

export function setUser(user: User | null) {
  current = user;
}

/** Solo rutas internas: evita usar ?redirect= para mandar al usuario fuera. */
export function safeRedirect(target: string | undefined): string {
  return target && target.startsWith('/') && !target.startsWith('//') ? target : '/';
}
