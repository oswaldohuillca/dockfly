import { createFileRoute, redirect } from '@tanstack/react-router';

import { Servers } from '@/components/servers';
import { can } from '@/lib/roles';

export const Route = createFileRoute('/_app/servers')({
  // Solo administradores (la API también lo exige).
  beforeLoad: ({ context }) => {
    if (!can(context.user, 'admin')) throw redirect({ to: '/' });
  },
  component: Servers,
});
