import { useEffect, useState } from 'react';
import { ArrowUpCircle, CheckCircle2, ExternalLink, Loader2, TriangleAlert } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { FormDialog } from '@/components/form-dialog';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, type UpdateInfo } from '@/lib/api';
import { useSubmit } from '@/lib/use-form';
import { useResource } from '@/lib/use-resource';

const busyStates = ['backup', 'pulling', 'restarting'];

/** Versión de Dockfly y botón para actualizar a la última. */
export function SystemUpdate() {
  const { t } = useLingui();
  const [confirming, setConfirming] = useState(false);
  // Tras lanzar la actualización, el panel deja de responder unos segundos.
  const [waitingFor, setWaitingFor] = useState('');
  const { data: info, reload } = useResource(api.getUpdate, { interval: waitingFor ? 2000 : undefined });

  // Cuando vuelve a responder con la versión nueva, se recarga el panel.
  useEffect(() => {
    if (!waitingFor) return;
    const id = setInterval(async () => {
      const status = await api.status().catch(() => null);
      if (status?.version === waitingFor) {
        clearInterval(id);
        toast.success(t`Dockfly updated to ${waitingFor}`);
        setTimeout(() => window.location.reload(), 1000);
      }
    }, 3000);
    return () => clearInterval(id);
  }, [waitingFor, t]);
  // Si falló antes de reiniciar (copia, descarga), se deja de esperar.
  useEffect(() => {
    if (waitingFor && info?.state === 'failed') setWaitingFor('');
  }, [waitingFor, info?.state]);

  const reasons: Record<NonNullable<UpdateInfo['reason']>, string> = {
    not_in_docker: t`Dockfly isn't running in a Docker container with access to Docker, so it can't update itself.`,
    dev_version: t`This is a development build (${info?.current ?? ''}): update it by pulling the image again.`,
    pinned_tag: t`The container uses an image tag that isn't a version. Switch it to :latest to update from here.`,
    no_socket: t`The Docker socket isn't mounted from the host, so the update can't recreate the container.`,
  };
  const latest = info?.latest?.version ?? '';
  const updating = !!waitingFor || busyStates.includes(info?.state ?? '');

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Dockfly version</Trans>
        </CardTitle>
        <CardDescription>
          {info ? <Trans>You're running {info.current}.</Trans> : <Trans>Checking for updates…</Trans>}
        </CardDescription>
        {info?.can_update && !updating && (
          <CardAction>
            <Button onClick={() => setConfirming(true)}>
              <ArrowUpCircle /> <Trans>Update to {latest}</Trans>
            </Button>
          </CardAction>
        )}
      </CardHeader>
      {info && (
        <CardContent className="space-y-3 text-sm">
          {updating ? (
            <p className="flex items-center gap-2">
              <Loader2 className="size-4 animate-spin" />
              {info.state === 'backup' && <Trans>Backing up the data…</Trans>}
              {info.state === 'pulling' && <Trans>Downloading the new version…</Trans>}
              {(info.state === 'restarting' || (waitingFor && !busyStates.includes(info.state))) && (
                <Trans>Restarting with the new version; the dashboard will reload by itself…</Trans>
              )}
            </p>
          ) : info.available ? (
            <p className="flex flex-wrap items-center gap-2">
              <ArrowUpCircle className="size-4 text-primary" />
              <Trans>Version {latest} is available.</Trans>
              {info.latest?.url && (
                <a className="inline-flex items-center gap-1 underline" href={info.latest.url} target="_blank" rel="noreferrer">
                  <Trans>What's new</Trans> <ExternalLink className="size-3" />
                </a>
              )}
            </p>
          ) : (
            info.latest && (
              <p className="flex items-center gap-2 text-muted-foreground">
                <CheckCircle2 className="size-4" /> <Trans>You have the latest version.</Trans>
              </p>
            )
          )}
          {info.available && info.reason && (
            <div className="space-y-2 text-muted-foreground">
              <p>{reasons[info.reason]}</p>
              <pre className="overflow-x-auto rounded bg-muted p-3 font-mono text-xs">docker compose pull && docker compose up -d</pre>
            </div>
          )}
          {info.state === 'failed' && info.error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{info.error}</AlertDescription>
            </Alert>
          )}
        </CardContent>
      )}
      {confirming && info && (
        <UpdateDialog
          version={latest}
          onClose={() => setConfirming(false)}
          onStarted={() => {
            setConfirming(false);
            setWaitingFor(latest);
            reload();
          }}
        />
      )}
    </Card>
  );
}

function UpdateDialog({ version, onClose, onStarted }: { version: string; onClose: () => void; onStarted: () => void }) {
  const [password, setPassword] = useState('');
  const { submit, busy, error } = useSubmit(async () => {
    await api.startUpdate(password);
    onStarted();
  });
  return (
    <FormDialog
      title={<Trans>Update Dockfly to {version}?</Trans>}
      description={
        <Trans>
          Dockfly backs up its data to /data/pre-update.tar.gz, downloads the new version and restarts with it. The
          dashboard stops responding for a few seconds; your sites and apps keep working. If the new version doesn&apos;t
          start, it goes back to the current one.
        </Trans>
      }
      submitLabel={<Trans>Update</Trans>}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      error={error}
    >
      <div className="space-y-2">
        <Label htmlFor="update-password">
          <Trans>Your password</Trans>
        </Label>
        <Input
          id="update-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
          autoComplete="current-password"
        />
      </div>
    </FormDialog>
  );
}
