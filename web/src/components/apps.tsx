import { ph } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';
import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { Boxes, GitBranch, Lock, Plus } from 'lucide-react';
import { toast } from 'sonner';

import { AppDialog } from '@/components/app-dialog';
import { AppStateDot } from '@/components/app-status';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { api, errorMessage, type App } from '@/lib/api';
import { useStatus } from '@/lib/status';
import { useCan } from '@/lib/roles';
import { useServerNames } from '@/lib/use-servers';

export function Apps() {
  const { t } = useLingui();
  const servers = useServerNames();
  const canEdit = useCan('deployer');
  const navigate = useNavigate();
  const status = useStatus();
  const [apps, setApps] = useState<App[] | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(() => {
    api.listApps().then(setApps).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(load, 10_000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-lg">Apps</CardTitle>
          <CardDescription>
            <Trans>
              Containers that Dockfly deploys and manages: APIs, databases, services… from an image or a Dockerfile.
            </Trans>
          </CardDescription>
          <CardAction>
            {canEdit && (
              <Button onClick={() => setCreating(true)} disabled={status !== null && !status.docker_available}>
                <Plus /> <Trans>New app</Trans>
              </Button>
            )}
          </CardAction>
        </CardHeader>
        <CardContent>
          {status && !status.docker_available && (
            <p className="mb-4 text-sm text-destructive">
              <Trans>
                Docker is unavailable: mount <code className="font-mono">/var/run/docker.sock</code> into the Dockfly
                container to use apps.
              </Trans>
            </p>
          )}
          {apps === null ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              <Trans>Loading…</Trans>
            </p>
          ) : apps.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-14 text-center">
              <div className="grid size-12 place-items-center rounded-full bg-muted">
                <Boxes className="size-6 text-muted-foreground" />
              </div>
              <div>
                <p className="font-medium">
                  <Trans>No apps yet</Trans>
                </p>
                <p className="text-sm text-muted-foreground">
                  <Trans>Start from a template: PostgreSQL, Redis, n8n…</Trans>
                </p>
              </div>
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {apps.map((a) => (
                <Link
                  key={a.id}
                  to="/apps/$appId"
                  params={{ appId: a.id }}
                  className="rounded-lg border p-4 transition-colors hover:border-primary/50 hover:bg-muted/40"
                >
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-medium">
                      {a.name}
                      {a.server_id && (
                        <span className="ml-1.5 text-xs font-normal text-muted-foreground">
                          {t`on ${ph({ server: servers[a.server_id] ?? t`another server` })}`}
                        </span>
                      )}
                    </span>
                    <AppStateDot app={a} />
                  </div>
                  <div className="mt-1 flex items-center gap-1 truncate font-mono text-xs text-muted-foreground">
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
        </CardContent>
      </Card>
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
