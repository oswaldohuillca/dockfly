import { createFileRoute, redirect } from '@tanstack/react-router';

import { Integrations } from '@/components/integrations';
import { can } from '@/lib/roles';

export const Route = createFileRoute('/_app/integrations')({
  // Administrators only (the API requires it too).
  beforeLoad: ({ context }) => {
    if (!can(context.user, 'admin')) throw redirect({ to: '/' });
  },
  component: Integrations,
});
