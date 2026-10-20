import { useCallback, useEffect, useState } from 'react';
import { DatabaseBackup, Download, Loader2, RotateCcw, Trash2 } from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { toast } from 'sonner';

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
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { api, errorMessage, type DBBackup, type DBBackupPolicy, type DBBackups } from '@/lib/api';
import { formatBytes, formatDate, timeAgo } from '@/lib/format';
import { useCan } from '@/lib/roles';

const engineName = { postgres: 'PostgreSQL', mysql: 'MySQL/MariaDB', redis: 'Redis' } as const;

const intervals = [
  { hours: 1, label: 'Cada hora' },
  { hours: 6, label: 'Cada 6 horas' },
  { hours: 12, label: 'Cada 12 horas' },
  { hours: 24, label: 'Cada día' },
  { hours: 168, label: 'Cada semana' },
];

/** Copias de seguridad de una app de base de datos. */
export function DBBackupsPanel({ appId, data: initial }: { appId: number; data: DBBackups }) {
  const canEdit = useCan('deployer');
  const [data, setData] = useState(initial);
  const [policy, setPolicy] = useState<DBBackupPolicy>(initial.policy);
  const [restoring, setRestoring] = useState<DBBackup | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .listDBBackups(appId)
      .then(setData)
      .catch(() => {});
  }, [appId]);
  // Mientras haya una copia en marcha, se refresca la lista.
  const running = data.backups.some((b) => b.status === 'running');
  useEffect(() => {
    if (!running) return;
    const t = setInterval(load, 2000);
    return () => clearInterval(t);
  }, [running, load]);

  async function savePolicy(next: DBBackupPolicy) {
    setPolicy(next);
    try {
      await api.putDBBackupPolicy(appId, next);
      toast.success(next.enabled ? 'Copias automáticas activadas' : 'Copias automáticas desactivadas');
      load();
    } catch (e) {
      toast.error(errorMessage(e));
      setPolicy(data.policy);
    }
  }

  async function backupNow() {
    setBusy(true);
    try {
      await api.startDBBackup(appId);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }

  async function restore(b: DBBackup) {
    setBusy(true);
    try {
      await api.restoreDBBackup(appId, b.id);
      toast.success('Base de datos restaurada', { description: `Copia del ${formatDate(b.created_at)}` });
    } catch (e) {
      toast.error(errorMessage(e), { duration: 15000 });
    } finally {
      setBusy(false);
      setRestoring(null);
    }
  }

  async function remove(b: DBBackup) {
    try {
      await api.deleteDBBackup(appId, b.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  if (!data.engine) return null;
  const engine = data.engine;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Copias de seguridad</CardTitle>
        <CardDescription>
          Copias completas de {engineName[engine]}, comprimidas, en <code className="font-mono">{data.location}</code>.
          El almacenamiento (p. ej. un bucket S3) se configura en{' '}
          <Link to="/settings" className="underline">
            Ajustes
          </Link>
          .
        </CardDescription>
        {canEdit && (
          <CardAction>
            <Button onClick={backupNow} disabled={busy || running}>
              {running ? <Loader2 className="animate-spin" /> : <DatabaseBackup />} Copia ahora
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="space-y-4 px-0">
        <div className="flex flex-wrap items-end gap-4 px-6">
          <div className="flex items-center gap-2">
            <Switch
              id="backup-enabled"
              checked={policy.enabled}
              disabled={!canEdit}
              onCheckedChange={(enabled) => savePolicy({ ...policy, enabled })}
            />
            <Label htmlFor="backup-enabled">Copias automáticas</Label>
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">Frecuencia</Label>
            <Select
              value={String(policy.interval_hours)}
              disabled={!canEdit}
              onValueChange={(v) => savePolicy({ ...policy, interval_hours: Number(v) })}
            >
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {intervals.map((i) => (
                  <SelectItem key={i.hours} value={String(i.hours)}>
                    {i.label}
                  </SelectItem>
                ))}
                {!intervals.some((i) => i.hours === policy.interval_hours) && (
                  <SelectItem value={String(policy.interval_hours)}>Cada {policy.interval_hours} h</SelectItem>
                )}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="backup-keep" className="text-xs text-muted-foreground">
              Conservar
            </Label>
            <Input
              id="backup-keep"
              type="number"
              min={1}
              max={365}
              className="w-24"
              value={policy.keep}
              disabled={!canEdit}
              onChange={(e) => setPolicy({ ...policy, keep: Number(e.target.value) })}
              onBlur={() => policy.keep !== data.policy.keep && policy.keep >= 1 && savePolicy(policy)}
            />
          </div>
        </div>

        {data.backups.length === 0 ? (
          <p className="px-6 py-4 text-center text-sm text-muted-foreground">Todavía no hay copias.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-6">Copia</TableHead>
                <TableHead>Tamaño</TableHead>
                <TableHead>Origen</TableHead>
                <TableHead className="pr-6" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.backups.map((b) => (
                <TableRow key={b.id}>
                  <TableCell className="pl-6">
                    <div className="flex items-center gap-2">
                      <span title={formatDate(b.created_at)}>{timeAgo(b.created_at)}</span>
                      {b.status === 'running' && (
                        <Badge variant="secondary">
                          <Loader2 className="animate-spin" /> En marcha
                        </Badge>
                      )}
                      {b.status === 'failed' && <Badge variant="destructive">Falló</Badge>}
                    </div>
                    {b.status === 'failed' && <div className="max-w-md truncate text-xs text-destructive">{b.error}</div>}
                  </TableCell>
                  <TableCell>{b.status === 'success' ? formatBytes(b.size) : '—'}</TableCell>
                  <TableCell className="text-muted-foreground">{b.trigger === 'schedule' ? 'Programada' : 'Manual'}</TableCell>
                  <TableCell className="pr-6 text-right">
                    {b.status === 'success' && canEdit && (
                      <>
                        <Button variant="ghost" size="sm" asChild>
                          <a href={api.dbBackupURL(appId, b.id)} download>
                            <Download /> Descargar
                          </a>
                        </Button>
                        {data.can_restore && (
                          <Button variant="ghost" size="sm" onClick={() => setRestoring(b)}>
                            <RotateCcw /> Restaurar
                          </Button>
                        )}
                      </>
                    )}
                    {b.status !== 'running' && canEdit && (
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => remove(b)}
                        aria-label="Borrar la copia"
                      >
                        <Trash2 />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        {!data.can_restore && (
          <p className="px-6 text-xs text-muted-foreground">
            Las copias de Redis se restauran a mano: para la app, copia el archivo descomprimido como{' '}
            <code className="font-mono">dump.rdb</code> en su volumen de datos y vuelve a arrancarla.
          </p>
        )}
      </CardContent>

      <AlertDialog open={restoring !== null} onOpenChange={(open) => !open && setRestoring(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>¿Restaurar la copia del {restoring && formatDate(restoring.created_at)}?</AlertDialogTitle>
            <AlertDialogDescription>
              Los datos actuales se sustituyen por los de la copia: lo que haya cambiado desde entonces se perderá. Haz
              antes una copia si no estás seguro.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>Cancelar</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={(e) => {
              e.preventDefault();
              if (restoring) restore(restoring);
            }}>
              {busy && <Loader2 className="animate-spin" />}
              Restaurar
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
