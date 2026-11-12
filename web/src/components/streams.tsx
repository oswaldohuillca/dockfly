import { useState } from 'react';
import { ArrowRight, Pencil, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';
import { i18n } from '@lingui/core';
import { msg } from '@lingui/core/macro';
import { Trans, useLingui } from '@lingui/react/macro';

import { useConfirm } from '@/components/confirm';
import { FormDialog } from '@/components/form-dialog';
import { ResourceState } from '@/components/resource-state';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { api, type Stream, type StreamInput } from '@/lib/api';
import { useForm, useSubmit } from '@/lib/use-form';
import { useAction, useResource } from '@/lib/use-resource';
import { cn } from '@/lib/utils';
import { useCan } from '@/lib/roles';

const portLine = (s: Pick<Stream, 'listen_port' | 'protocol'>) =>
  `"${s.listen_port}:${s.listen_port}${s.protocol === 'udp' ? '/udp' : ''}"`;

function restartedToast(res: { traefik_restarted: boolean }) {
  if (res.traefik_restarted) {
    toast.info(i18n._(msg`Traefik restarted to listen on the new port`), {
      description: i18n._(msg`There may have been an outage of ~1 s.`),
    });
  }
}

export function Streams() {
  const { t } = useLingui();
  const canEdit = useCan('admin');
  const { data: list = [], loading, error, reload } = useResource(api.listStreams);
  const [editing, setEditing] = useState<Stream | 'new' | null>(null);
  const run = useAction();
  const { confirm, dialog } = useConfirm();

  const toggle = (s: Stream) =>
    run(async () => restartedToast(await api.updateStream(s.id, { ...s, enabled: !s.enabled })), { then: reload });

  async function remove(s: Stream) {
    const port = s.listen_port;
    if (await confirm({ title: t`Delete the stream on port ${port}?` })) {
      await run(async () => restartedToast(await api.deleteStream(s.id)), { then: reload });
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>TCP/UDP streams</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Forward a port to a non-web service: databases, game servers, MQTT… Adding or removing a port restarts
            Traefik (~1 s outage).
          </Trans>
        </CardDescription>
        <CardAction>
          {canEdit && (
            <Button variant="outline" onClick={() => setEditing('new')}>
              <Plus /> <Trans>Add</Trans>
            </Button>
          )}
        </CardAction>
      </CardHeader>
      {(loading || error) && (
        <CardContent>
          <ResourceState loading={loading} error={error} onRetry={reload} />
        </CardContent>
      )}
      {list.length > 0 && (
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-6">
                  <Trans>Port</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Target</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Published</Trans>
                </TableHead>
                <TableHead>
                  <Trans>Enabled</Trans>
                </TableHead>
                <TableHead className="pr-6" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((s) => (
                <TableRow key={s.id} className={cn(!s.enabled && 'opacity-55')}>
                  <TableCell className="pl-6">
                    <span className="font-mono">{s.listen_port}</span>{' '}
                    <Badge variant="outline" className="uppercase">
                      {s.protocol}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-1.5 font-mono text-xs">
                      <ArrowRight className="size-3.5 text-muted-foreground" />
                      {s.forward_host}:{s.forward_port}
                    </span>
                  </TableCell>
                  <TableCell>
                    {s.published === null ? (
                      <span className="text-xs text-muted-foreground">—</span>
                    ) : s.published ? (
                      <Badge variant="secondary" className="text-emerald-700 dark:text-emerald-400">
                        <Trans>Yes</Trans>
                      </Badge>
                    ) : (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Badge variant="secondary" className="gap-1 text-amber-700">
                            <TriangleAlert /> <Trans>No</Trans>
                          </Badge>
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs">
                          <Trans>
                            The Dockfly container doesn't publish this port: add <code>{portLine(s)}</code> to the
                            ports section of its docker-compose.yml and run docker compose up -d.
                          </Trans>
                        </TooltipContent>
                      </Tooltip>
                    )}
                  </TableCell>
                  <TableCell>
                    <Switch checked={s.enabled} onCheckedChange={() => toggle(s)} aria-label={t`Enabled`} />
                  </TableCell>
                  <TableCell className="pr-6 text-right">
                    <Button variant="ghost" size="icon-sm" onClick={() => setEditing(s)} aria-label={t`Edit`}>
                      <Pencil />
                    </Button>
                    <Button variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" onClick={() => remove(s)} aria-label={t`Delete`}>
                      <Trash2 />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      )}
      {editing && (
        <StreamDialog
          stream={editing === 'new' ? undefined : editing}
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

function StreamDialog({ stream, onClose, onSaved }: { stream?: Stream; onClose: () => void; onSaved: () => void }) {
  const { form, set } = useForm<StreamInput>(() => ({
    protocol: stream?.protocol ?? 'tcp', listen_port: stream?.listen_port ?? 0,
    forward_host: stream?.forward_host ?? '', forward_port: stream?.forward_port ?? 0, enabled: stream?.enabled ?? true,
  }));
  const { t } = useLingui();
  const { submit, busy, error } = useSubmit(async () => {
    const res = stream ? await api.updateStream(stream.id, form) : await api.createStream(form);
    toast.success(t`Stream saved`);
    restartedToast(res);
    onSaved();
  });

  return (
    <FormDialog
      className="sm:max-w-md"
      title={stream ? <Trans>Edit stream</Trans> : <Trans>New stream</Trans>}
      description={<Trans>The target can be a Dockfly app by name (e.g. postgres), a container or an IP.</Trans>}
      onClose={onClose}
      onSubmit={submit}
      busy={busy}
      error={error}
    >
      <ToggleGroup type="single" variant="outline" spacing={0} className="w-full" value={form.protocol}
        onValueChange={(v) => v && set('protocol', v as 'tcp' | 'udp')}>
        <ToggleGroupItem value="tcp" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">TCP</ToggleGroupItem>
        <ToggleGroupItem value="udp" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">UDP</ToggleGroupItem>
      </ToggleGroup>
      <div className="space-y-2">
        <Label htmlFor="st-port">
          <Trans>Listen port</Trans>
        </Label>
        <Input id="st-port" type="number" min={1} max={65535} value={form.listen_port || ''} onChange={(e) => set('listen_port', Number(e.target.value))} placeholder="5432" required />
      </div>
      <div className="grid grid-cols-[1fr_7rem] gap-3">
        <div className="space-y-2">
          <Label htmlFor="st-host">
            <Trans>Target</Trans>
          </Label>
          <Input id="st-host" className="font-mono" value={form.forward_host} onChange={(e) => set('forward_host', e.target.value)} placeholder="postgres" required />
        </div>
        <div className="space-y-2">
          <Label htmlFor="st-fport">
            <Trans>Port</Trans>
          </Label>
          <Input id="st-fport" type="number" min={1} max={65535} value={form.forward_port || ''} onChange={(e) => set('forward_port', Number(e.target.value))} placeholder="5432" required />
        </div>
      </div>
      {form.listen_port > 0 && (
        <p className="text-xs text-muted-foreground">
          <Trans>
            Remember to publish the port on the Dockfly container: <code className="font-mono">{portLine(form)}</code>{' '}
            in the ports section of its docker-compose.yml.
          </Trans>
        </p>
      )}
    </FormDialog>
  );
}
