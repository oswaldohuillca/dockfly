import { createFileRoute, notFound } from '@tanstack/react-router';

import { SiteDetail } from '@/components/site-detail';

export const Route = createFileRoute('/_app/sites/$siteId')({
  // El parámetro llega como texto: lo validamos y convertimos a número.
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
  // key: al cambiar de sitio, el estado de la página empieza de cero.
  return <SiteDetail key={siteId} id={siteId} />;
}
