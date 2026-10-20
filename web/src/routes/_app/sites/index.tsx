import { createFileRoute } from '@tanstack/react-router';

import { Sites } from '@/components/sites';

export const Route = createFileRoute('/_app/sites/')({
  component: Sites,
});
