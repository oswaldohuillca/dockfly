import { useLingui } from '@lingui/react/macro';

import { Gauge } from '@/components/gauge';
import { ResourceState } from '@/components/resource-state';
import { api } from '@/lib/api';
import { formatBytes } from '@/lib/format';
import { useResource } from '@/lib/use-resource';

/** CPU y memoria del contenedor de una app, al día cada 5 s. */
export function AppUsage({ appId }: { appId: number }) {
  const { t } = useLingui();
  const { data: u, loading, error, reload } = useResource(() => api.appStats(appId), { interval: 5000 });

  if (!u) return <ResourceState loading={loading} error={error} onRetry={reload} className="py-6" />;

  return (
    <div className="mx-auto grid max-w-md grid-cols-2 gap-4">
      <Gauge label="CPU" percent={u.cpu_percent} detail={t`of all the server's cores`} />
      <Gauge
        label={t`Memory`}
        percent={u.mem_limit > 0 ? (100 * u.mem_used) / u.mem_limit : 0}
        detail={t`${formatBytes(u.mem_used)} of ${formatBytes(u.mem_limit)}`}
      />
    </div>
  );
}
