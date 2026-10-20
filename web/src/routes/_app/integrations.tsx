import { createFileRoute, redirect } from '@tanstack/react-router';

import { Integrations } from '@/components/integrations';
import { can } from '@/lib/roles';

export const Route = createFileRoute('/_app/integrations')({
  // Solo administradores (la API también lo exige).
  beforeLoad: ({ context }) => {
    if (!can(context.user, 'admin')) throw redirect({ to: '/' });
  },
  component: Integrations,
});
