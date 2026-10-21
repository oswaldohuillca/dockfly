import { msg } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';
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
  { hours: 1, label: msg`Every hour` },
  { hours: 6, label: msg`Every 6 hours` },
  { hours: 12, label: msg`Every 12 hours` },
  { hours: 24, label: msg`Every day` },
  { hours: 168, label: msg`Every week` },
];

/** Copias de seguridad de una app de base de datos. */
export function DBBackupsPanel({ appId, data: initial }: { appId: number; data: DBBackups }) {
  const { t, i18n } = useLingui();
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
      toast.success(next.enabled ? t`Automatic backups enabled` : t`Automatic backups disabled`);
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
      const date = formatDate(b.created_at);
      toast.success(t`Database restored`, { description: t`Backup from ${date}` });
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
  const engineLabel = engineName[engine];
  const location = data.location;
  const customHours = policy.interval_hours;
  const restoringDate = restoring ? formatDate(restoring.created_at) : '';

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Trans>Backups</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Full, compressed {engineLabel} backups in <code className="font-mono">{location}</code>. Storage (e.g.
            an S3 bucket) is configured in{' '}
            <Link to="/settings" className="underline">
              Settings
            </Link>
            .
          </Trans>
        </CardDescription>
        {canEdit && (
          <CardAction>
            <Button onClick={backupNow} disabled={busy || running}>
              {running ? <Loader2 className="animate-spin" /> : <DatabaseBackup />} <Trans>Back up now</Trans>
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
            <Label htmlFor="backup-enabled">
              <Trans>Automatic backups</Trans>
            </Label>
          </div>
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">
              <Trans>Frequency</Trans>
            </Label>
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
                    {i18n._(i.label)}
                  </SelectItem>
                ))}
                {!intervals.some((i) => i.hours === policy.interval_hours) && (
                  <SelectItem value={String(policy.interval_hours)}>
                    <Trans>Every {customHours} h</Trans>
                  </SelectItem>
                )}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="backup-keep" className="text-xs text-muted-foreground">
              <Trans>Keep</Trans>
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
          <p className="px-6 py-4 text-center text-sm text-muted-foreground">
            <Trans>No backups yet.</Trans>
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-6">
                  <Trans>Backup</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Size</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Trigger</Trans>
                </TableHead>
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
                          <Loader2 className="animate-spin" /> <Trans>Running</Trans>
                        </Badge>
                      )}
                      {b.status === 'failed' && <Badge variant="destructive">
                          <Trans context="backup">Failed</Trans>
                        </Badge>}
                    </div>
                    {b.status === 'failed' && <div className="max-w-md truncate text-xs text-destructive">{b.error}</div>}
                  </TableCell>
                  <TableCell>{b.status === 'success' ? formatBytes(b.size) : '—'}</TableCell>
                  <TableCell className="text-muted-foreground">{b.trigger === 'schedule' ? t`Scheduled` : t`Manual`}</TableCell>
                  <TableCell className="pr-6 text-right">
                    {b.status === 'success' && canEdit && (
                      <>
                        <Button variant="ghost" size="sm" asChild>
                          <a href={api.dbBackupURL(appId, b.id)} download>
                            <Download /> <Trans>Download</Trans>
                          </a>
                        </Button>
                        {data.can_restore && (
                          <Button variant="ghost" size="sm" onClick={() => setRestoring(b)}>
                            <RotateCcw /> <Trans>Restore</Trans>
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
                        aria-label={t`Delete backup`}
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
            <Trans>
              Redis backups are restored manually: stop the app, copy the decompressed file as{' '}
              <code className="font-mono">dump.rdb</code> into its data volume and start it again.
            </Trans>
          </p>
        )}
      </CardContent>

      <AlertDialog open={restoring !== null} onOpenChange={(open) => !open && setRestoring(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              <Trans>Restore the backup from {restoringDate}?</Trans>
            </AlertDialogTitle>
            <AlertDialogDescription>
              <Trans>
                The current data is replaced with the backup's: anything changed since then will be lost. Back up
                first if you're not sure.
              </Trans>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>
              <Trans>Cancel</Trans>
            </AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={(e) => {
              e.preventDefault();
              if (restoring) restore(restoring);
            }}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Restore</Trans>
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
