import { ph } from '@lingui/core/macro';
import { Plural, Trans, useLingui } from '@lingui/react/macro';
import { useCallback, useEffect, useState } from 'react';
import { Copy, KeyRound, Loader2, Plus, Server, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type RemoteServer } from '@/lib/api';
import { timeAgo } from '@/lib/format';

/** Servidores remotos, cada uno con su agente. */
export function Servers() {
  const { t } = useLingui();
  const [servers, setServers] = useState<RemoteServer[]>([]);
  const [adding, setAdding] = useState(false);
  const [join, setJoin] = useState<{ name: string; command: string; warning: string } | null>(null);

  const load = useCallback(() => {
    api
      .listServers()
      .then(setServers)
      .catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(() => {
    load();
    // El estado de conexión cambia solo: se refresca cada pocos segundos.
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load]);

  async function rotate(s: RemoteServer) {
    try {
      const res = await api.rotateServerToken(s.id);
      setJoin({ name: s.name, command: res.command, warning: res.warning });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(s: RemoteServer) {
    try {
      await api.deleteServer(s.id);
      toast.success(t`${ph({ name: s.name })} deleted`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">
          <Trans>Servers</Trans>
        </CardTitle>
        <CardDescription>
          <Trans>
            Deploy apps to other machines. Each server runs a Dockfly agent with its own Traefik: the agent connects to
            this panel (no open ports needed) and, if the panel goes down, its apps keep running.
          </Trans>
        </CardDescription>
        <CardAction>
          <Button onClick={() => setAdding(true)}>
            <Plus /> <Trans>Add server</Trans>
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y">
          <li className="flex flex-wrap items-center gap-3 px-6 py-3">
            <Server className="size-4 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <div className="font-medium">
                <Trans>This server</Trans>
              </div>
              <div className="text-xs text-muted-foreground">
                <Trans>The panel's own server. Proxy hosts, sites and Compose apps.</Trans>
              </div>
            </div>
            <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
              <Trans>Connected</Trans>
            </Badge>
          </li>
          {servers.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
              <Server className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="font-medium">{s.name}</div>
                <div className="text-xs text-muted-foreground">
                  {s.last_seen_at
                    ? `${s.last_addr} · Dockfly ${s.version || '?'} · ${s.online ? t`connected` : t`seen ${ph({ ago: timeAgo(s.last_seen_at) })}`}`
                    : t`Waiting for its agent to connect…`}
                  {' · '}
                  <Plural value={s.apps} one="# app" other="# apps" />
                </div>
              </div>
              {s.online ? (
                <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                  <Trans>Connected</Trans>
                </Badge>
              ) : s.last_seen_at ? (
                <Badge variant="destructive">
                  <Trans>Disconnected</Trans>
                </Badge>
              ) : (
                <Badge variant="outline">
                  <Loader2 className="animate-spin" /> <Trans>Pending</Trans>
                </Badge>
              )}
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => rotate(s)}
                title={t`New install command (invalidates the previous one)`}
                aria-label={t`New token for ${ph({ name: s.name })}`}
              >
                <KeyRound />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                onClick={() => remove(s)}
                aria-label={t`Delete ${ph({ name: s.name })}`}
              >
                <Trash2 />
              </Button>
            </li>
          ))}
        </ul>
      </CardContent>
      {adding && (
        <AddServerDialog
          onClose={() => setAdding(false)}
          onCreated={(info) => {
            setAdding(false);
            setJoin(info);
            load();
          }}
        />
      )}
      {join && <JoinDialog {...join} onClose={() => setJoin(null)} />}
    </Card>
  );
}

function AddServerDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (info: { name: string; command: string; warning: string }) => void;
}) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res = await api.createServer(name);
      onCreated({ name: res.server.name, command: res.command, warning: res.warning });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>
              <Trans>Add server</Trans>
            </DialogTitle>
            <DialogDescription>
              <Trans>It only needs Docker. You'll run a command on it next.</Trans>
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="server-name">
              <Trans>Name</Trans>
            </Label>
            <Input
              id="server-name"
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
              placeholder="madrid-1"
              required
              autoFocus
            />
          </div>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              <Trans>Continue</Trans>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function JoinDialog({
  name,
  command,
  warning,
  onClose,
}: {
  name: string;
  command: string;
  warning: string;
  onClose: () => void;
}) {
  const { t } = useLingui();
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            <Trans context="server">Connect {name}</Trans>
          </DialogTitle>
          <DialogDescription>
            <Trans>
              Run this on the server (with Docker installed). The agent connects to this panel and will show as
              connected within a few seconds. The command contains the server's token: don't share it.
            </Trans>
          </DialogDescription>
        </DialogHeader>
        {warning ? (
          <Alert variant="destructive">
            <TriangleAlert />
            <AlertDescription>{warning}</AlertDescription>
          </Alert>
        ) : (
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">{command}</pre>
        )}
        <p className="text-xs text-muted-foreground">
          <Trans>
            It uses the server's ports 80 and 443 for its apps. The panel must be reachable from it: set its public URL
            in Integrations.
          </Trans>
        </p>
        <DialogFooter>
          <Button
            variant="outline"
            disabled={!command}
            onClick={() => navigator.clipboard.writeText(command).then(() => toast.success(t`Command copied`))}
          >
            <Copy /> <Trans>Copy</Trans>
          </Button>
          <Button onClick={onClose}>
            <Trans>Done</Trans>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
