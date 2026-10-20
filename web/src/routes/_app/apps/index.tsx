import { createFileRoute } from '@tanstack/react-router';

import { Apps } from '@/components/apps';

export const Route = createFileRoute('/_app/apps/')({
  component: Apps,
});
