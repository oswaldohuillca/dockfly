import { ph } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';
import { useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { Boxes, CircleCheck, CircleDashed, GitBranch, Lock, Plus, TriangleAlert } from 'lucide-react';

import { AppDialog } from '@/components/app-dialog';
import { AppStateDot } from '@/components/app-status';
import { ResourceState } from '@/components/resource-state';
import { PageHeader, Stat, StatGrid } from '@/components/page-header';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { api } from '@/lib/api';
import { useStatus } from '@/lib/status';
import { useCan } from '@/lib/roles';
import { useResource } from '@/lib/use-resource';
import { useServerNames } from '@/lib/use-servers';

export function Apps() {
  const { t } = useLingui();
  const servers = useServerNames();
  const canEdit = useCan('deployer');
  const navigate = useNavigate();
  const status = useStatus();
  const { data: apps, loading, error, reload } = useResource(api.listApps, { interval: 10_000 });
  const [creating, setCreating] = useState(false);
  const running = apps?.filter((a) => a.current && !a.stopped && a.state === 'running').length ?? 0;
  const problems = apps?.filter((a) => a.current && !a.stopped && a.state !== 'running').length ?? 0;

  return (
    <>
      <PageHeader
        title="Apps"
        description={
          <Trans>
            Containers that Dockfly deploys and manages: APIs, databases, services… from an image or a Dockerfile.
          </Trans>
        }
      >
        {canEdit && (
          <Button onClick={() => setCreating(true)} disabled={status !== null && !status.docker_available}>
            <Plus /> <Trans>New app</Trans>
          </Button>
        )}
      </PageHeader>
      {apps && apps.length > 0 && (
        <StatGrid>
          <Stat icon={Boxes} label={<Trans>Total apps</Trans>} value={apps.length} />
          <Stat icon={CircleCheck} tone="success" label={<Trans>Running</Trans>} value={running} />
          <Stat icon={TriangleAlert} tone={problems ? 'danger' : 'neutral'} label={<Trans>With problems</Trans>} value={problems} />
          <Stat icon={CircleDashed} tone="neutral" label={<Trans>Stopped or not deployed</Trans>} value={apps.length - running - problems} />
        </StatGrid>
      )}
      <div>
        <div>
          {status && !status.docker_available && (
            <p className="mb-4 text-sm text-destructive">
              <Trans>
                Docker is unavailable: mount <code className="font-mono">/var/run/docker.sock</code> into the Dockfly
                container to use apps.
              </Trans>
            </p>
          )}
          {!apps ? (
            <Card>
              <ResourceState loading={loading} error={error} onRetry={reload} />
            </Card>
          ) : apps.length === 0 ? (
            <Card className="flex flex-col items-center gap-3 py-16 text-center">
              <div className="grid size-12 place-items-center rounded-none bg-primary/10">
                <Boxes className="size-6 text-primary" />
              </div>
              <div>
                <p className="font-medium">
                  <Trans>No apps yet</Trans>
                </p>
                <p className="text-sm text-muted-foreground">
                  <Trans>Start from a template: PostgreSQL, Redis, n8n…</Trans>
                </p>
              </div>
            </Card>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {apps.map((a) => (
                <Link
                  key={a.id}
                  to="/apps/$appId"
                  params={{ appId: a.id }}
                  className="rounded-2xl bg-card p-5 shadow-card transition-all duration-150 hover:-translate-y-0.5 hover:shadow-lg"
                >
                  <div className="flex items-center gap-3">
                    <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                      <Boxes className="size-5" />
                    </span>
                    <span className="min-w-0 flex-1 truncate text-base font-semibold tracking-tight">
                      {a.name}
                      {a.server_id && (
                        <span className="ml-1.5 text-xs font-normal text-muted-foreground">
                          {t`on ${ph({ server: servers[a.server_id] ?? t`another server` })}`}
                        </span>
                      )}
                    </span>
                    <AppStateDot app={a} />
                  </div>
                  <div className="mt-4 flex items-center gap-1 truncate font-mono text-xs text-muted-foreground">
                    {a.source !== 'image' ? (
                      <>
                        <GitBranch className="size-3 shrink-0" /> {a.repo}
                      </>
                    ) : (
                      a.image
                    )}
                  </div>
                  <div className="mt-2 truncate text-sm text-muted-foreground">
                    {a.domains.length > 0 ? (
                      <span className="flex items-center gap-1">
                        {a.ssl && <Lock className="size-3" />}
                        {a.domains[0]}
                      </span>
                    ) : (
                      <span>
                        <Trans>Internal:</Trans>{' '}
                        <code className="font-mono text-xs">{a.name}{a.port ? `:${a.port}` : ''}</code>
                      </span>
                    )}
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>
      {creating && (
        <AppDialog
          onClose={() => setCreating(false)}
          onSaved={(a) => {
            setCreating(false);
            navigate({ to: '/apps/$appId', params: { appId: a.id } });
          }}
        />
      )}
    </>
  );
}
