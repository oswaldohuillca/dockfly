import { createFileRoute } from '@tanstack/react-router';

import { ProxyHosts } from '@/components/proxy-hosts';
import { useStatus } from '@/lib/status';

export const Route = createFileRoute('/_app/proxy-hosts')({
  component: ProxyHostsPage,
});

function ProxyHostsPage() {
  const status = useStatus();
  return <ProxyHosts dockerAvailable={status?.docker_available ?? false} />;
}
