import { createFileRoute, redirect } from '@tanstack/react-router';

import { Servers } from '@/components/servers';
import { can } from '@/lib/roles';

export const Route = createFileRoute('/_app/servers')({
  // Administrators only (the API requires it too).
  beforeLoad: ({ context }) => {
    if (!can(context.user, 'admin')) throw redirect({ to: '/' });
  },
  component: Servers,
});
