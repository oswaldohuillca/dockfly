import { useMemo, useState } from 'react';
import { Box, CircleCheck, CircleDashed, ExternalLink, Globe, Lock, Pencil, Plus, Search, Server, Trash2 } from 'lucide-react';
import type { MessageDescriptor } from '@lingui/core';
import { msg } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';

import { CertBadge } from '@/components/cert-badge';
import { useConfirm } from '@/components/confirm';
import { ProxyHostDialog } from '@/components/proxy-host-dialog';
import { compactNumber, formatMs, Sparkline, TrafficCard, useTrafficSummary } from '@/components/traffic';
import { ResourceState } from '@/components/resource-state';
import { PageHeader, Stat, StatGrid } from '@/components/page-header';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { api, type ProxyHost, type ProxyHostInput, type TargetType, type TrafficSummary } from '@/lib/api';
import { useCan } from '@/lib/roles';
import { useCertificates } from '@/lib/use-certificates';
import { useAction, useResource } from '@/lib/use-resource';
import { cn } from '@/lib/utils';

const targets: Record<TargetType, { label: MessageDescriptor; icon: typeof Box }> = {
  docker: { label: msg`Container`, icon: Box },
  host: { label: msg`This server`, icon: Server },
  external: { label: msg`External`, icon: Globe },
};

/** La API rechaza campos desconocidos: enviamos solo los editables. */
function toInput({ id: _id, created_at: _c, updated_at: _u, ...rest }: ProxyHost): ProxyHostInput {
  return rest;
}

export function ProxyHosts({ dockerAvailable }: { dockerAvailable: boolean }) {
  const { t, i18n } = useLingui();
  const traffic = useTrafficSummary();
  const [trafficOf, setTrafficOf] = useState<ProxyHost | null>(null);
  const canEdit = useCan('admin');
  const { data: hosts = [], setData: setHosts, loading, error, reload: load } = useResource(api.listProxyHosts);
  const [filter, setFilter] = useState('');
  // null = cerrado, {} = nuevo, host = editar
  const [editing, setEditing] = useState<Partial<ProxyHost> | null>(null);
  const certs = useCertificates();
  const run = useAction();
  const { confirm, dialog } = useConfirm();

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return hosts;
    return hosts.filter((h) => h.domains.some((d) => d.includes(q)) || h.forward_host.toLowerCase().includes(q));
  }, [hosts, filter]);

  async function toggle(h: ProxyHost) {
    // Actualización optimista: el switch responde al instante.
    setHosts((prev) => prev?.map((x) => (x.id === h.id ? { ...x, enabled: !h.enabled } : x)));
    if (!(await run(() => api.updateProxyHost(h.id, { ...toInput(h), enabled: !h.enabled })))) load();
  }

  async function remove(h: ProxyHost) {
    const ok = await confirm({
      title: <Trans>Delete {h.domains[0]}?</Trans>,
      description: <Trans>The domain will stop responding immediately. This action can't be undone.</Trans>,
    });
    if (!ok) return;
    const domain = h.domains[0];
    await run(() => api.deleteProxyHost(h.id), { success: t`${domain} deleted`, then: load });
  }

  return (
    <>
      <PageHeader title="Proxy Hosts" description={<Trans>Publish containers, apps on this server or external services.</Trans>}>
            <div className="relative">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder={t`Search…`}
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                aria-label={t`Search proxy hosts`}
                className="w-56 pl-8"
              />
            </div>
            {canEdit && (
              <Button onClick={() => setEditing({})}>
                <Plus /> <Trans>Add</Trans>
              </Button>
            )}
      </PageHeader>
      {hosts.length > 0 && (
        <StatGrid>
          <Stat icon={Globe} label={<Trans>Total hosts</Trans>} value={hosts.length} />
          <Stat icon={CircleCheck} tone="success" label={<Trans>Enabled</Trans>} value={hosts.filter((h) => h.enabled).length} />
          <Stat tone="accent" icon={Lock} label={<Trans>With SSL</Trans>} value={hosts.filter((h) => h.ssl).length} />
          <Stat icon={CircleDashed} tone="neutral" label={<Trans>Disabled</Trans>} value={hosts.filter((h) => !h.enabled).length} />
        </StatGrid>
      )}
      <Card className="py-2">

        <CardContent className="px-0">
          {loading || error ? (
            <ResourceState loading={loading} error={error} onRetry={load} />
          ) : hosts.length === 0 ? (
            <EmptyState onCreate={() => setEditing({})} />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-6">
                    <Trans>Domains</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Target</Trans>
                  </TableHead>
                  <TableHead>SSL</TableHead>
                  <TableHead>
                    <Trans>Traffic (24 h)</Trans>
                  </TableHead>
                  <TableHead>
                    <Trans>Enabled</Trans>
                  </TableHead>
                  <TableHead className="pr-6" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((h) => {
                  const target = targets[h.target_type];
                  return (
                    <TableRow key={h.id} className={cn(!h.enabled && 'opacity-55')}>
                      <TableCell className="pl-6">
                        {h.domains.map((d) => (
                          <a
                            key={d}
                            href={`${h.ssl ? 'https' : 'http'}://${d}`}
                            target="_blank"
                            rel="noreferrer"
                            className="group flex items-center gap-1 font-medium hover:underline"
                          >
                            {d}
                            <ExternalLink className="size-3 opacity-0 transition-opacity group-hover:opacity-60" />
                          </a>
                        ))}
                      </TableCell>
                      <TableCell>
                        <code className="rounded-none bg-muted px-1.5 py-0.5 font-mono text-xs">
                          {h.forward_scheme}://{h.forward_host}:{h.forward_port}
                        </code>
                        <div className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                          <target.icon className="size-3" /> {i18n._(target.label)}
                        </div>
                      </TableCell>
                      <TableCell>
                        {h.ssl ? (
                          <CertBadge domains={h.domains} certs={certs} />
                        ) : (
                          <Badge variant="outline" className="text-muted-foreground">
                            HTTP
                          </Badge>
                        )}
                      </TableCell>
                      <TableCell>
                        <TrafficCell summary={traffic[`ph-${h.id}`]} onClick={() => setTrafficOf(h)} />
                      </TableCell>
                      <TableCell>
                        <Switch
                          checked={h.enabled}
                          onCheckedChange={() => toggle(h)}
                          aria-label={h.enabled ? t`Disable` : t`Enable`}
                        />
                      </TableCell>
                      <TableCell className="pr-6 text-right">
                        <Button variant="ghost" size="icon-sm" onClick={() => setEditing(h)} aria-label={t`Edit`}>
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          className="text-destructive hover:text-destructive"
                          onClick={() => remove(h)}
                          aria-label={t`Delete`}
                        >
                          <Trash2 />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
                {visible.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
                      <Trans>No proxy hosts match “{filter}”.</Trans>
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {editing && (
        <ProxyHostDialog
          host={editing}
          dockerAvailable={dockerAvailable}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}

      <Dialog open={trafficOf !== null} onOpenChange={(open) => !open && setTrafficOf(null)}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{trafficOf?.domains[0]}</DialogTitle>
            <DialogDescription>
              {trafficOf && `${trafficOf.forward_scheme}://${trafficOf.forward_host}:${trafficOf.forward_port}`}
            </DialogDescription>
          </DialogHeader>
          {trafficOf && <TrafficCard owner={`ph-${trafficOf.id}`} bare />}
        </DialogContent>
      </Dialog>

      {dialog}
    </>
  );
}

function EmptyState({ onCreate }: { onCreate: () => void }) {
  const canEdit = useCan('admin');
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      <div className="grid size-12 place-items-center rounded-none bg-primary/10">
        <Globe className="size-6 text-primary" />
      </div>
      <div>
        <p className="font-medium">
          <Trans>No proxy hosts yet</Trans>
        </p>
        <p className="text-sm text-muted-foreground">
          <Trans>Create one to publish a container, an app on this server or an external service.</Trans>
        </p>
      </div>
      {canEdit && (
        <Button onClick={onCreate}>
          <Plus /> <Trans>Add proxy host</Trans>
        </Button>
      )}
    </div>
  );
}

/** Minigráfica y totales de las últimas 24 h de una ruta. */
export function TrafficCell({ summary, onClick }: { summary?: TrafficSummary; onClick: () => void }) {
  if (!summary || summary.requests === 0) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }
  const errors = summary.requests > 0 ? (summary.s5xx / summary.requests) * 100 : 0;
  return (
    <button type="button" onClick={onClick} className="flex items-center gap-2 rounded-none px-1 py-0.5 text-left hover:bg-muted">
      <Sparkline values={summary.spark} />
      <span className="text-xs leading-tight tabular-nums">
        {compactNumber(summary.requests)}
        <span className={cn('block', errors >= 5 ? 'text-destructive' : errors >= 1 ? 'text-amber-700' : 'text-muted-foreground')}>
          {errors > 0 ? `${errors.toFixed(1)} % 5xx` : formatMs(summary.p95_ms) + ' p95'}
        </span>
      </span>
    </button>
  );
}
