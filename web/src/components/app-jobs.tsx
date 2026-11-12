import { useState } from 'react';
import { Clock, Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';

import { CheckField } from '@/components/check-field';
import { useConfirm } from '@/components/confirm';
import { FormDialog } from '@/components/form-dialog';
import { ResourceState } from '@/components/resource-state';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, type AppJob } from '@/lib/api';
import { timeAgo } from '@/lib/format';
import { useSubmit } from '@/lib/use-form';
import { useAction, useResource } from '@/lib/use-resource';

/** Tareas programadas de una app: comandos que se ejecutan en su contenedor según un cron. */
export function AppJobs({ appId, canEdit, canRun }: { appId: number; canEdit: boolean; canRun: boolean }) {
  const { t } = useLingui();
  // Se refresca solo: el resultado de una ejecución llega cuando termina.
  const { data: jobs = [], loading, error, reload } = useResource(() => api.listAppJobs(appId), { interval: 5000 });
  const [editing, setEditing] = useState<AppJob | 'new' | null>(null);
  const [output, setOutput] = useState<number | null>(null);
  const run = useAction();
  const { confirm, dialog } = useConfirm();

  async function remove(job: AppJob) {
    const command = job.command;
    if (!(await confirm({ title: t`Delete the task "${command}"?` }))) return;
    return run(() => api.deleteAppJob(appId, job.id), { success: t`Task deleted`, then: reload });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Scheduled tasks</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>Commands that run inside the app's container on a schedule: cleanups, imports, a framework's scheduler.</Trans>
        </CardDescription>
        {canEdit && (
          <CardAction>
            <Button variant="outline" onClick={() => setEditing('new')}>
              <Plus /> <Trans>New task</Trans>
            </Button>
          </CardAction>
        )}
      </CardHeader>
      <CardContent className="px-0">
        {loading || error ? (
          <ResourceState loading={loading} error={error} onRetry={reload} />
        ) : jobs.length === 0 ? (
          <p className="px-6 py-2 text-sm text-muted-foreground">
            <Trans>No scheduled tasks yet.</Trans>
          </p>
        ) : (
          <ul className="divide-y">
            {jobs.map((job) => (
              <li key={job.id} className="px-6 py-3">
                <div className="flex flex-wrap items-center gap-3">
                  <Clock className="size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <code className="block truncate font-mono text-sm font-medium">{job.command}</code>
                    <div className="text-xs text-muted-foreground">
                      <code className="font-mono">{job.schedule}</code> (UTC) ·{' '}
                      {job.last_run_at ? <Trans>last run {timeAgo(job.last_run_at)}</Trans> : <Trans>never run</Trans>}
                    </div>
                  </div>
                  {!job.enabled && (
                    <Badge variant="secondary">
                      <Trans>Paused</Trans>
                    </Badge>
                  )}
                  {job.last_run_at && job.last_exit_code === null && (
                    <Badge variant="outline">
                      <Trans>Running…</Trans>
                    </Badge>
                  )}
                  {job.last_exit_code === 0 && (
                    <Badge variant="outline" className="text-emerald-700 dark:text-emerald-400">
                      <Trans>Succeeded</Trans>
                    </Badge>
                  )}
                  {job.last_exit_code !== null && job.last_exit_code !== 0 && (
                    <Badge variant="destructive">
                      {job.last_exit_code > 0 ? t`Failed (code ${job.last_exit_code})` : t`Failed`}
                    </Badge>
                  )}
                  {job.last_run_at && (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-expanded={output === job.id}
                      onClick={() => setOutput(output === job.id ? null : job.id)}
                    >
                      {output === job.id ? <Trans>Hide output</Trans> : <Trans>Show output</Trans>}
                    </Button>
                  )}
                  {canRun && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => run(() => api.runAppJob(appId, job.id), { success: t`Task started`, then: reload })}
                    >
                      <Play /> <Trans>Run now</Trans>
                    </Button>
                  )}
                  {canEdit && (
                    <>
                      <Button variant="ghost" size="icon-sm" onClick={() => setEditing(job)} aria-label={t`Edit task ${job.command}`}>
                        <Pencil />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => remove(job)}
                        aria-label={t`Delete task ${job.command}`}
                      >
                        <Trash2 />
                      </Button>
                    </>
                  )}
                </div>
                {output === job.id && (
                  <pre className="mt-3 max-h-72 overflow-auto bg-zinc-950 p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap text-zinc-100">
                    {job.last_output || t`No output.`}
                  </pre>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {editing && (
        <JobDialog
          appId={appId}
          job={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
      {dialog}
    </Card>
  );
}

function JobDialog({ appId, job, onClose, onSaved }: { appId: number; job?: AppJob; onClose: () => void; onSaved: () => void }) {
  const { t } = useLingui();
  const [command, setCommand] = useState(job?.command ?? '');
  const [schedule, setSchedule] = useState(job?.schedule ?? '0 3 * * *');
  const [enabled, setEnabled] = useState(job?.enabled ?? true);
  const { submit, busy, error } = useSubmit(async () => {
    await api.saveAppJob(appId, { command, schedule, enabled }, job?.id);
    onSaved();
  });

  return (
    <FormDialog
      className="sm:max-w-md"
      title={job ? <Trans>Edit scheduled task</Trans> : <Trans>New scheduled task</Trans>}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      error={error}
    >
      <div className="space-y-2">
        <Label htmlFor="job-command">
          <Trans>Command</Trans>
        </Label>
        <Input
          id="job-command"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="php artisan schedule:run"
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          required
          autoFocus
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="job-schedule">
          <Trans>Schedule</Trans>
        </Label>
        <Input
          id="job-schedule"
          value={schedule}
          onChange={(e) => setSchedule(e.target.value)}
          className="font-mono"
          autoComplete="off"
          spellCheck={false}
          aria-describedby="job-schedule-hint"
          required
        />
        <p id="job-schedule-hint" className="text-xs leading-relaxed text-muted-foreground">
          <Trans>
            Cron expression in UTC: minute, hour, day of month, month, day of week. <code className="font-mono whitespace-nowrap">0 3 * * *</code>{' '}
            is every day at 03:00; <code className="font-mono whitespace-nowrap">*/15 * * * *</code> is every 15 minutes.
          </Trans>
        </p>
      </div>
      <CheckField id="job-enabled" checked={enabled} onChange={setEnabled} label={t`Enabled`} hint={t`A paused task keeps its settings but doesn't run.`} />
    </FormDialog>
  );
}
