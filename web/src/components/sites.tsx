import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import { FileCode2, Lock, Plus } from 'lucide-react';
import { Trans } from '@lingui/react/macro';
import { toast } from 'sonner';

import { SiteDialog } from '@/components/site-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { api, errorMessage, type Site } from '@/lib/api';
import { timeAgo } from '@/lib/format';
import { useCan } from '@/lib/roles';

export function Sites() {
  const canEdit = useCan('deployer');
  const navigate = useNavigate();
  const [sites, setSites] = useState<Site[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      setSites(await api.listSites());
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className="text-lg">
            <Trans>Sites</Trans>
          </CardTitle>
          <CardDescription>
            <Trans>Publish static sites by uploading your build folder.</Trans>
          </CardDescription>
          <CardAction>
            {canEdit && (
              <Button onClick={() => setCreating(true)}>
                <Plus /> <Trans>New site</Trans>
              </Button>
            )}
          </CardAction>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              <Trans>Loading…</Trans>
            </p>
          ) : sites.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-14 text-center">
              <div className="grid size-12 place-items-center rounded-full bg-muted">
                <FileCode2 className="size-6 text-muted-foreground" />
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
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {sites.map((site) => (
                <Link
                  key={site.id}
                  to="/sites/$siteId"
                  params={{ siteId: site.id }}
                  className="rounded-lg border p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted/40"
                >
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-medium">{site.name}</span>
                    {site.active_deployment ? (
                      <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                        <Trans context="deployment">Live</Trans>
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="text-muted-foreground">
                        <Trans>Not deployed</Trans>
                      </Badge>
                    )}
                  </div>
                  <div className="mt-1 flex items-center gap-1 truncate text-sm text-muted-foreground">
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
        </CardContent>
      </Card>

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
