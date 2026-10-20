import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from '@tanstack/react-router';
import {
  ArrowLeft,
  ExternalLink,
  GitBranch,
  GitCommitHorizontal,
  Lock,
  Pencil,
  Play,
  RotateCcw,
  RotateCw,
  Square,
  Trash2,
} from 'lucide-react';
import { toast } from 'sonner';

import { AppDialog } from '@/components/app-dialog';
import { AppStateDot, DeployStatusBadge } from '@/components/app-status';
import { CertBadge } from '@/components/cert-badge';
import { CheckField } from '@/components/check-field';
import { LiveLog } from '@/components/live-log';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { api, ApiError, appDeployFinished, errorMessage, type App, type AppDeployment } from '@/lib/api';
import { formatDate, timeAgo } from '@/lib/format';
import { useCertificates } from '@/lib/use-certificates';

const triggerLabel = { manual: 'Manual', push: 'Push', rollback: 'Vuelta atrás' };

export function AppDetail({ id }: { id: number }) {
  const navigate = useNavigate();
  const certs = useCertificates();
  const [app, setApp] = useState<App | null>(null);
  const [deployments, setDeployments] = useState<AppDeployment[]>([]);
  const [notFound, setNotFound] = useState(false);
  const [editing, setEditing] = useState(false);
  const [viewing, setViewing] = useState<AppDeployment | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [removeData, setRemoveData] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const res = await api.getApp(id);
      setApp(res.app);
      setDeployments(res.deployments);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setNotFound(true);
      else toast.error(errorMessage(e));
    }
  }, [id]);

  // Más a menudo mientras hay un despliegue en marcha.
  const active = deployments.some((d) => !appDeployFinished(d));
  useEffect(() => {
    load();
    const t = setInterval(load, active ? 2500 : 10_000);
    return () => clearInterval(t);
  }, [load, active]);

  async function run(label: string, fn: () => Promise<unknown>) {
    setBusy(true);
    try {
      await fn();
      toast.success(label);
      await load();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function deploy() {
    try {
      const d = await api.deployApp(id);
      await load();
      setViewing(d);
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove() {
    try {
      await api.deleteApp(id, removeData);
      toast.success(`${app?.name} eliminada${removeData ? ' junto con sus datos' : ''}`);
      navigate({ to: '/apps' });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (notFound) {
    return (
      <div className="py-20 text-center">
        <p className="font-medium">Esta app no existe</p>
        <Button variant="link" asChild>
          <Link to="/apps">Volver a las apps</Link>
        </Button>
      </div>
    );
  }
  if (!app) return <p className="py-20 text-center text-sm text-muted-foreground">Cargando…</p>;

  const deployed = !!app.current;
  const url = (d: string) => `${app.ssl ? 'https' : 'http'}://${d}`;

  return (
    <div className="space-y-4">
      <Button variant="ghost" size="sm" className="-ml-2" asChild>
        <Link to="/apps">
          <ArrowLeft /> Apps
        </Link>
      </Button>

      <Card>
        <CardHeader className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <CardTitle className="flex flex-wrap items-center gap-3 text-xl">
              {app.name}
              <AppStateDot app={app} />
              {app.ssl && app.domains.length > 0 && <CertBadge domains={app.domains} certs={certs} />}
            </CardTitle>
            <CardDescription className="space-y-1">
              <div className="flex items-center gap-1 font-mono text-xs">
                {app.source === 'git' ? (
                  <>
                    <GitBranch className="size-3.5" /> {app.repo} · {app.branch}
                  </>
                ) : (
                  app.image
                )}
              </div>
              <div className="flex flex-wrap gap-x-4 gap-y-1">
                {app.domains.map((d) => (
                  <a key={d} href={url(d)} target="_blank" rel="noreferrer" className="flex items-center gap-1 font-medium text-foreground hover:underline">
                    {app.ssl && <Lock className="size-3" />}
                    {d}
                    <ExternalLink className="size-3 opacity-60" />
                  </a>
                ))}
                <span>
                  En la red interna: <code className="font-mono">{app.name}{app.port ? `:${app.port}` : ''}</code>
                </span>
              </div>
            </CardDescription>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" onClick={deploy} disabled={active}>
              <Play /> {deployed ? 'Desplegar' : 'Desplegar por primera vez'}
            </Button>
            {deployed && (
              <>
                <Button size="sm" variant="outline" disabled={busy} onClick={() => run('Reiniciada', () => api.appAction(id, 'restart'))}>
                  <RotateCw /> Reiniciar
                </Button>
                {app.stopped ? (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => run('En marcha', () => api.appAction(id, 'start'))}>
                    <Play /> Iniciar
                  </Button>
                ) : (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => run('Detenida', () => api.appAction(id, 'stop'))}>
                    <Square /> Detener
                  </Button>
                )}
              </>
            )}
            <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
              <Pencil /> Configurar
            </Button>
            <Button size="sm" variant="outline" className="text-destructive hover:text-destructive" onClick={() => setDeleting(true)}>
              <Trash2 />
            </Button>
          </div>
        </CardHeader>
      </Card>

      <Tabs defaultValue="deployments">
        <TabsList>
          <TabsTrigger value="deployments">Despliegues</TabsTrigger>
          <TabsTrigger value="logs" disabled={!deployed}>
            Logs
          </TabsTrigger>
        </TabsList>

        <TabsContent value="deployments">
          <Card>
            <CardContent className="px-0">
              {deployments.length === 0 ? (
                <p className="px-6 py-6 text-center text-sm text-muted-foreground">Todavía no hay despliegues.</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="pl-6">Despliegue</TableHead>
                      <TableHead>Versión</TableHead>
                      <TableHead>Origen</TableHead>
                      <TableHead>Fecha</TableHead>
                      <TableHead className="pr-6" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {deployments.map((d) => (
                      <TableRow key={d.id} className="cursor-pointer" onClick={() => setViewing(d)}>
                        <TableCell className="pl-6">
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-sm">#{d.id}</span>
                            <DeployStatusBadge status={d.status} />
                          </div>
                        </TableCell>
                        <TableCell className="max-w-72">
                          {d.commit_sha ? (
                            <div className="flex items-center gap-1.5 truncate">
                              <GitCommitHorizontal className="size-3.5 shrink-0 text-muted-foreground" />
                              <code className="font-mono text-xs text-muted-foreground">{d.commit_sha.slice(0, 7)}</code>
                              <span className="truncate">{d.commit_message}</span>
                            </div>
                          ) : (
                            <code className="truncate font-mono text-xs text-muted-foreground">{d.image || '—'}</code>
                          )}
                          {d.status === 'failed' && <div className="truncate text-xs text-destructive">{d.error}</div>}
                        </TableCell>
                        <TableCell className="text-muted-foreground">{triggerLabel[d.trigger]}</TableCell>
                        <TableCell className="text-muted-foreground" title={formatDate(d.created_at)}>
                          {timeAgo(d.created_at)}
                        </TableCell>
                        <TableCell className="pr-6 text-right" onClick={(e) => e.stopPropagation()}>
                          {d.status === 'replaced' && d.image && (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={active}
                              onClick={async () => {
                                try {
                                  setViewing(await api.redeploy(id, d.id));
                                  load();
                                } catch (e) {
                                  toast.error(errorMessage(e));
                                }
                              }}
                            >
                              <RotateCcw /> Volver a esta versión
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="logs">
          {deployed && (
            <LiveLog
              key={app.current!.container}
              url={api.appLogsURL(id)}
              emptyText="Sin salida todavía…"
              className="h-[60svh] overflow-auto rounded-md bg-zinc-950 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-100"
            />
          )}
        </TabsContent>
      </Tabs>

      {editing && (
        <AppDialog
          app={app}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            load();
          }}
        />
      )}
      {viewing && <DeploymentLog deployment={viewing} onClose={() => setViewing(null)} onChange={load} />}

      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Eliminar {app.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Se detiene y se borra su contenedor. Sus dominios dejarán de responder.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {app.volumes.length > 0 && (
            <CheckField
              id="remove-data"
              checked={removeData}
              onChange={setRemoveData}
              label="Borrar también sus datos (volúmenes)"
              hint={`${app.volumes.map((v) => v.name).join(', ')}: no se puede deshacer.`}
            />
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancelar</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              Eliminar
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function DeploymentLog({ deployment, onClose, onChange }: { deployment: AppDeployment; onClose: () => void; onChange: () => void }) {
  const [d, setD] = useState(deployment);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[90svh] flex-col sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            Despliegue #{d.id} <DeployStatusBadge status={d.status} />
          </DialogTitle>
          <DialogDescription>
            {d.commit_sha ? `${d.commit_sha.slice(0, 7)} ${d.commit_message}` : d.image} · {formatDate(d.created_at)}
          </DialogDescription>
        </DialogHeader>
        <LiveLog
          url={api.appDeploymentLogsURL(d.id)}
          emptyText={d.status === 'queued' ? 'Esperando turno en la cola…' : 'Cargando…'}
          onStatus={(x) => setD(x as AppDeployment)}
          onEnd={(x) => {
            setD(x as AppDeployment);
            onChange();
          }}
        />
        {!appDeployFinished(d) && (
          <div className="flex justify-end">
            <Button
              variant="outline"
              onClick={() => api.cancelAppDeployment(d.id).catch((e) => toast.error(errorMessage(e)))}
            >
              <Square /> Cancelar despliegue
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
