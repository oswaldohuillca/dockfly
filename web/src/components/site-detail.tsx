import { useCallback, useEffect, useState } from 'react';
import { ArrowLeft, ExternalLink, Lock, Pencil, RotateCcw, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { ProviderIcon } from '@/components/brand-icons';
import { Dropzone, selectionSummary } from '@/components/dropzone';
import { SiteDialog } from '@/components/site-dialog';
import { SiteGit } from '@/components/site-git';
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
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { api, ApiError, errorMessage, type Deployment, type Site } from '@/lib/api';
import type { Selection } from '@/lib/collect-files';
import { formatBytes, formatDate, plural, timeAgo } from '@/lib/format';
import { navigate } from '@/lib/router';

export function SiteDetail({ id }: { id: number }) {
  const [site, setSite] = useState<Site | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [notFound, setNotFound] = useState(false);
  const [editing, setEditing] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [uploading, setUploading] = useState('');

  const load = useCallback(async () => {
    try {
      const res = await api.getSite(id);
      setSite(res.site);
      setDeployments(res.deployments);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setNotFound(true);
      else toast.error(errorMessage(e));
    }
  }, [id]);

  useEffect(() => {
    load();
  }, [load]);

  async function upload(sel: Selection) {
    setUploading(selectionSummary(sel));
    setProgress(0);
    try {
      const dep = await api.uploadDeployment(id, sel.payload, setProgress);
      toast.success(`Despliegue #${dep.id} publicado`, {
        description: `${plural(dep.file_count, 'archivo', 'archivos')} · ${formatBytes(dep.total_bytes)}`,
      });
      await load();
    } catch (e) {
      toast.error('No se pudo publicar', { description: errorMessage(e) });
    } finally {
      setProgress(null);
    }
  }

  async function activate(dep: Deployment) {
    try {
      await api.activateDeployment(id, dep.id);
      toast.success(`Despliegue #${dep.id} publicado de nuevo`);
      await load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function removeDeployment(dep: Deployment) {
    try {
      await api.deleteDeployment(id, dep.id);
      await load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function removeSite() {
    try {
      await api.deleteSite(id);
      toast.success(`${site?.name} eliminado`);
      navigate('/sites');
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (notFound) {
    return (
      <div className="py-20 text-center">
        <p className="font-medium">Este sitio no existe</p>
        <Button variant="link" onClick={() => navigate('/sites')}>
          Volver a los sitios
        </Button>
      </div>
    );
  }
  if (!site) return <p className="py-20 text-center text-sm text-muted-foreground">Cargando…</p>;

  const url = (d: string) => `${site.ssl ? 'https' : 'http'}://${d}`;

  return (
    <div className="space-y-4">
      <Button variant="ghost" size="sm" onClick={() => navigate('/sites')} className="-ml-2">
        <ArrowLeft /> Sitios
      </Button>

      <Card>
        <CardHeader className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-1.5">
            <CardTitle className="flex items-center gap-2 text-xl">
              {site.name}
              {site.spa && <Badge variant="outline">SPA</Badge>}
            </CardTitle>
            <CardDescription className="flex flex-wrap gap-x-4 gap-y-1">
              {site.domains.map((d) => (
                <a
                  key={d}
                  href={url(d)}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-1 font-medium text-foreground hover:underline"
                >
                  {site.ssl && <Lock className="size-3" />}
                  {d}
                  <ExternalLink className="size-3 opacity-60" />
                </a>
              ))}
            </CardDescription>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
              <Pencil /> Editar
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 /> Eliminar
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          <Dropzone onSelect={upload} progress={progress} uploading={uploading} />
        </CardContent>
      </Card>

      <SiteGit site={site} onChange={load} />

      <Card>
        <CardHeader>
          <CardTitle>Despliegues</CardTitle>
          <CardDescription>Se conservan los 10 más recientes. Publica cualquiera para hacer rollback.</CardDescription>
        </CardHeader>
        <CardContent className="px-0">
          {deployments.length === 0 ? (
            <p className="px-6 py-6 text-center text-sm text-muted-foreground">
              Aún no hay despliegues. Sube tu primera versión arriba.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-6">Despliegue</TableHead>
                  <TableHead>Fecha</TableHead>
                  <TableHead>Archivos</TableHead>
                  <TableHead>Tamaño</TableHead>
                  <TableHead className="pr-6" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {deployments.map((dep) => {
                  const active = dep.id === site.active_deployment_id;
                  return (
                    <TableRow key={dep.id}>
                      <TableCell className="pl-6">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-sm">#{dep.id}</span>
                          {active && (
                            <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                              Publicado
                            </Badge>
                          )}
                        </div>
                        {dep.source === 'upload' ? (
                          <div className="mt-0.5 text-xs text-muted-foreground">Subida manual</div>
                        ) : (
                          <div className="mt-0.5 flex max-w-80 items-center gap-1.5 text-xs text-muted-foreground">
                            <ProviderIcon provider={dep.source} className="size-3" />
                            <code className="font-mono">{dep.commit_sha.slice(0, 7)}</code>
                            <span className="truncate">{dep.commit_message}</span>
                          </div>
                        )}
                      </TableCell>
                      <TableCell>
                        <Tooltip>
                          <TooltipTrigger className="cursor-default">{timeAgo(dep.created_at)}</TooltipTrigger>
                          <TooltipContent>{formatDate(dep.created_at)}</TooltipContent>
                        </Tooltip>
                      </TableCell>
                      <TableCell>{dep.file_count}</TableCell>
                      <TableCell>{formatBytes(dep.total_bytes)}</TableCell>
                      <TableCell className="pr-6 text-right">
                        {!active && (
                          <>
                            <Button variant="ghost" size="sm" onClick={() => activate(dep)}>
                              <RotateCcw /> Publicar
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              className="text-destructive hover:text-destructive"
                              onClick={() => removeDeployment(dep)}
                              aria-label={`Eliminar despliegue ${dep.id}`}
                            >
                              <Trash2 />
                            </Button>
                          </>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {editing && (
        <SiteDialog
          site={site}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            load();
          }}
        />
      )}

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Eliminar {site.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Sus dominios dejarán de responder y se borrarán todos sus despliegues. No se puede deshacer.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancelar</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={removeSite}>
              Eliminar
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
