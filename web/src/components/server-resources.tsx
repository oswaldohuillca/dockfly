import { Trans, useLingui } from '@lingui/react/macro';

import { Gauge } from '@/components/gauge';
import { ResourceState } from '@/components/resource-state';
import { api } from '@/lib/api';
import { formatBytes, formatDuration } from '@/lib/format';
import { useResource } from '@/lib/use-resource';

const pct = (used: number, total: number) => (total > 0 ? (100 * used) / total : 0);

/** CPU, memoria y disco de un servidor (0 = el del panel), al día cada 5 s. */
export function ServerResources({ serverId }: { serverId: number }) {
  const { t } = useLingui();
  const { data: s, loading, error, reload } = useResource(() => api.serverStats(serverId), { interval: 5000 });

  if (!s) return <ResourceState loading={loading} error={error} onRetry={reload} className="py-6" />;

  const cores = s.cores;
  const facts: [string, string][] = [
    [t`Load average`, `${s.load1.toFixed(2)} · ${s.load5.toFixed(2)} · ${s.load15.toFixed(2)}`],
    [t`Uptime`, formatDuration(s.uptime_seconds)],
    [t`Swap`, s.swap_total > 0 ? t`${formatBytes(s.swap_used)} of ${formatBytes(s.swap_total)}` : t`none`],
  ];
  return (
    <div className="grid items-center gap-x-8 gap-y-6 md:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <div className="grid grid-cols-3 gap-4">
        <Gauge label="CPU" percent={s.cpu_percent} detail={<Trans>{cores} cores</Trans>} />
        <Gauge
          label={t`Memory`}
          percent={pct(s.mem_used, s.mem_total)}
          detail={t`${formatBytes(s.mem_used)} of ${formatBytes(s.mem_total)}`}
        />
        <Gauge
          label={t`Disk`}
          percent={pct(s.disk_used, s.disk_total)}
          detail={t`${formatBytes(s.disk_used)} of ${formatBytes(s.disk_total)}`}
        />
      </div>
      <dl className="grid gap-3 text-sm">
        {facts.map(([k, v]) => (
          <div key={k} className="flex items-baseline justify-between gap-4 border-b pb-3 last:border-0 last:pb-0">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="font-medium tabular-nums">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
