import { useCallback, useEffect, useState } from 'react';
import { FileCode2, Lock, Plus } from 'lucide-react';
import { toast } from 'sonner';

import { SiteDialog } from '@/components/site-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { api, errorMessage, type Site } from '@/lib/api';
import { timeAgo } from '@/lib/format';
import { navigate } from '@/lib/router';

export function Sites() {
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
          <CardTitle className="text-lg">Sitios</CardTitle>
          <CardDescription>Publica sitios estáticos subiendo la carpeta de tu build.</CardDescription>
          <CardAction>
            <Button onClick={() => setCreating(true)}>
              <Plus /> Nuevo sitio
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="py-8 text-center text-sm text-muted-foreground">Cargando…</p>
          ) : sites.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-14 text-center">
              <div className="grid size-12 place-items-center rounded-full bg-muted">
                <FileCode2 className="size-6 text-muted-foreground" />
              </div>
              <div>
                <p className="font-medium">Todavía no hay sitios</p>
                <p className="text-sm text-muted-foreground">
                  Crea uno y sube tu <code className="font-mono">dist/</code>: se publica al instante, con SSL y
                  rollback.
                </p>
              </div>
              <Button onClick={() => setCreating(true)}>
                <Plus /> Nuevo sitio
              </Button>
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {sites.map((site) => (
                <button
                  key={site.id}
                  onClick={() => navigate(`/sites/${site.id}`)}
                  className="rounded-lg border p-4 text-left transition-colors hover:border-primary/50 hover:bg-muted/40"
                >
                  <div className="flex items-start justify-between gap-2">
                    <span className="font-medium">{site.name}</span>
                    {site.active_deployment ? (
                      <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                        Publicado
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="text-muted-foreground">
                        Sin desplegar
                      </Badge>
                    )}
                  </div>
                  <div className="mt-1 flex items-center gap-1 truncate text-sm text-muted-foreground">
                    {site.ssl && <Lock className="size-3 shrink-0" />}
                    {site.domains[0]}
                    {site.domains.length > 1 && ` +${site.domains.length - 1}`}
                  </div>
                  <div className="mt-3 text-xs text-muted-foreground">
                    {site.active_deployment
                      ? `Despliegue #${site.active_deployment.id} · ${timeAgo(site.active_deployment.created_at)}`
                      : `Creado ${timeAgo(site.created_at)}`}
                  </div>
                </button>
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
            navigate(`/sites/${site.id}`);
          }}
        />
      )}
    </>
  );
}
