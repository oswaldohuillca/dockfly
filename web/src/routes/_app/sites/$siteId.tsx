import { createFileRoute, notFound } from '@tanstack/react-router';

import { SiteDetail } from '@/components/site-detail';

export const Route = createFileRoute('/_app/sites/$siteId')({
  // The param arrives as text: we validate it and turn it into a number.
  params: {
    parse: ({ siteId }) => {
      const id = Number(siteId);
      if (!Number.isInteger(id) || id < 1) throw notFound();
      return { siteId: id };
    },
    stringify: ({ siteId }) => ({ siteId: String(siteId) }),
  },
  component: SitePage,
});

function SitePage() {
  const { siteId } = Route.useParams();
  // key: when switching sites, the page's state starts from scratch.
  return <SiteDetail key={siteId} id={siteId} />;
}
