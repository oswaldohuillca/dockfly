import { useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { CircleCheck, CircleDashed, FileCode2, Lock, Plus } from 'lucide-react';
import { Trans } from '@lingui/react/macro';

import { SiteDialog } from '@/components/site-dialog';
import { ResourceState } from '@/components/resource-state';
import { PageHeader, Stat, StatGrid } from '@/components/page-header';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { api } from '@/lib/api';
import { timeAgo } from '@/lib/format';
import { useCan } from '@/lib/roles';
import { useResource } from '@/lib/use-resource';

export function Sites() {
  const canEdit = useCan('deployer');
  const navigate = useNavigate();
  const { data: sites = [], loading, error, reload } = useResource(api.listSites);
  const [creating, setCreating] = useState(false);
  const live = sites.filter((s) => s.active_deployment).length;

  return (
    <>
      <PageHeader title={<Trans>Sites</Trans>} description={<Trans>Publish static sites by uploading your build folder.</Trans>}>
        {canEdit && (
          <Button onClick={() => setCreating(true)}>
            <Plus /> <Trans>New site</Trans>
          </Button>
        )}
      </PageHeader>
      {sites.length > 0 && (
        <StatGrid>
          <Stat icon={FileCode2} label={<Trans>Total sites</Trans>} value={sites.length} />
          <Stat icon={CircleCheck} tone="success" label={<Trans context="deployment">Live</Trans>} value={live} />
          <Stat icon={CircleDashed} tone="neutral" label={<Trans>Not deployed</Trans>} value={sites.length - live} />
          <Stat tone="accent" icon={Lock} label={<Trans>With SSL</Trans>} value={sites.filter((s) => s.ssl).length} />
        </StatGrid>
      )}
      <div>
        <div>
          {loading || error ? (
            <Card>
              <ResourceState loading={loading} error={error} onRetry={reload} />
            </Card>
          ) : sites.length === 0 ? (
            <Card className="flex flex-col items-center gap-3 py-16 text-center">
              <div className="grid size-12 place-items-center rounded-full bg-primary/10">
                <FileCode2 className="size-6 text-primary" />
              </div>
              <div>
                <p className="font-medium">
                  <Trans>No sites yet</Trans>
                </p>
                <p className="text-sm text-muted-foreground">
                  <Trans>
                    Create one and upload your <code className="font-mono">dist/</code>: it goes live instantly, with
                    SSL and rollback.
                  </Trans>
                </p>
              </div>
              {canEdit && (
                <Button onClick={() => setCreating(true)}>
                  <Plus /> <Trans>New site</Trans>
                </Button>
              )}
            </Card>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {sites.map((site) => (
                <Link
                  key={site.id}
                  to="/sites/$siteId"
                  params={{ siteId: site.id }}
                  className="rounded-2xl bg-card p-5 shadow-card transition-all duration-150 hover:-translate-y-0.5 hover:shadow-lg"
                >
                  <div className="flex items-center gap-3">
                    <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                      <FileCode2 className="size-5" />
                    </span>
                    <span className="min-w-0 flex-1 truncate text-base font-semibold tracking-tight">{site.name}</span>
                    {site.active_deployment ? (
                      <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
                        <Trans context="deployment">Live</Trans>
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="text-muted-foreground">
                        <Trans>Not deployed</Trans>
                      </Badge>
                    )}
                  </div>
                  <div className="mt-4 flex items-center gap-1 truncate text-sm text-muted-foreground">
                    {site.ssl && <Lock className="size-3 shrink-0" />}
                    {site.domains[0]}
                    {site.domains.length > 1 && ` +${site.domains.length - 1}`}
                  </div>
                  <div className="mt-3 text-xs text-muted-foreground">
                    {site.active_deployment ? (
                      <Trans>
                        Deployment #{site.active_deployment.id} · {timeAgo(site.active_deployment.created_at)}
                      </Trans>
                    ) : (
                      <Trans>Created {timeAgo(site.created_at)}</Trans>
                    )}
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>

      {creating && (
        <SiteDialog
          onClose={() => setCreating(false)}
          onSaved={(site) => {
            setCreating(false);
            navigate({ to: '/sites/$siteId', params: { siteId: site.id } });
          }}
        />
      )}
    </>
  );
}
