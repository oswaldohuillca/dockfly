import { api, type User } from '@/lib/api';

// The session's user, checked just once and reused across navigations.
// undefined = not checked yet.
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

/** Internal routes only: stops ?redirect= from sending the user off-site. */
export function safeRedirect(target: string | undefined): string {
  return target && target.startsWith('/') && !target.startsWith('//') ? target : '/';
}
