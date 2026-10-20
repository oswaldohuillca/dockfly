import { createFileRoute } from '@tanstack/react-router';

import { ProxyHosts } from '@/components/proxy-hosts';
import { Redirects } from '@/components/redirects';
import { Streams } from '@/components/streams';
import { useStatus } from '@/lib/status';

export const Route = createFileRoute('/_app/proxy-hosts')({
  component: ProxyHostsPage,
});

function ProxyHostsPage() {
  const status = useStatus();
  return (
    <div className="space-y-4">
      <ProxyHosts dockerAvailable={status?.docker_available ?? false} />
      <Redirects />
      <Streams />
    </div>
  );
}
