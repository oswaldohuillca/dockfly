import { createFileRoute, notFound } from '@tanstack/react-router';

import { AppDetail } from '@/components/app-detail';

export const Route = createFileRoute('/_app/apps/$appId')({
  params: {
    parse: ({ appId }) => {
      const id = Number(appId);
      if (!Number.isInteger(id) || id < 1) throw notFound();
      return { appId: id };
    },
    stringify: ({ appId }) => ({ appId: String(appId) }),
  },
  component: AppPage,
});

function AppPage() {
  const { appId } = Route.useParams();
  return <AppDetail key={appId} id={appId} />;
}
