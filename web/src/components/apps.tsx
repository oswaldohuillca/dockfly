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

export function Apps() {
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
            Contenedores que Dockfly despliega y gestiona: APIs, bases de datos, servicios… desde una imagen o un Dockerfile.
          </CardDescription>
          <CardAction>
            {canEdit && (
              <Button onClick={() => setCreating(true)} disabled={status !== null && !status.docker_available}>
                <Plus /> Nueva app
              </Button>
            )}
          </CardAction>
        </CardHeader>
        <CardContent>
          {status && !status.docker_available && (
            <p className="mb-4 text-sm text-destructive">
              Docker no está disponible: monta <code className="font-mono">/var/run/docker.sock</code> en el contenedor de
              Dockfly para usar apps.
            </p>
          )}
          {apps === null ? (
            <p className="py-8 text-center text-sm text-muted-foreground">Cargando…</p>
          ) : apps.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-14 text-center">
              <div className="grid size-12 place-items-center rounded-full bg-muted">
                <Boxes className="size-6 text-muted-foreground" />
              </div>
              <div>
                <p className="font-medium">Todavía no hay apps</p>
                <p className="text-sm text-muted-foreground">Empieza con una plantilla: PostgreSQL, Redis, n8n…</p>
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
                    <span className="font-medium">{a.name}</span>
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
                        Interna: <code className="font-mono text-xs">{a.name}{a.port ? `:${a.port}` : ''}</code>
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
