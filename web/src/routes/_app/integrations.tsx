import { createFileRoute } from '@tanstack/react-router';

import { Integrations } from '@/components/integrations';

export const Route = createFileRoute('/_app/integrations')({
  component: Integrations,
});
