import { useCallback, useEffect, useState } from 'react';
import { Copy, KeyRound, Loader2, Plus, Server, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type RemoteServer } from '@/lib/api';
import { timeAgo } from '@/lib/format';

/** Servidores remotos, cada uno con su agente. */
export function Servers() {
  const [servers, setServers] = useState<RemoteServer[]>([]);
  const [adding, setAdding] = useState(false);
  const [join, setJoin] = useState<{ name: string; command: string } | null>(null);

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
      setJoin({ name: s.name, command: res.command });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(s: RemoteServer) {
    try {
      await api.deleteServer(s.id);
      toast.success(`${s.name} eliminado`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Servidores</CardTitle>
        <CardDescription>
          Despliega apps en otras máquinas. Cada servidor ejecuta un agente de Dockfly con su propio Traefik: el agente
          se conecta a este panel (no hace falta abrir puertos) y, si el panel se cae, sus apps siguen funcionando.
        </CardDescription>
        <CardAction>
          <Button onClick={() => setAdding(true)}>
            <Plus /> Añadir servidor
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y">
          <li className="flex flex-wrap items-center gap-3 px-6 py-3">
            <Server className="size-4 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <div className="font-medium">Este servidor</div>
              <div className="text-xs text-muted-foreground">El del panel. Proxy hosts, sitios y apps de Compose.</div>
            </div>
            <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
              Conectado
            </Badge>
          </li>
          {servers.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
              <Server className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="font-medium">{s.name}</div>
                <div className="text-xs text-muted-foreground">
                  {s.last_seen_at
                    ? `${s.last_addr} · Dockfly ${s.version || '?'} · ${s.online ? 'conectado' : `visto ${timeAgo(s.last_seen_at)}`}`
                    : 'Esperando a que se conecte su agente…'}
                  {' · '}
                  {s.apps === 1 ? '1 app' : `${s.apps} apps`}
                </div>
              </div>
              {s.online ? (
                <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                  Conectado
                </Badge>
              ) : s.last_seen_at ? (
                <Badge variant="destructive">Desconectado</Badge>
              ) : (
                <Badge variant="outline">
                  <Loader2 className="animate-spin" /> Pendiente
                </Badge>
              )}
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => rotate(s)}
                title="Nuevo comando de instalación (invalida el anterior)"
                aria-label={`Nuevo token para ${s.name}`}
              >
                <KeyRound />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                className="text-destructive hover:text-destructive"
                onClick={() => remove(s)}
                aria-label={`Eliminar ${s.name}`}
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
          onCreated={(name, command) => {
            setAdding(false);
            setJoin({ name, command });
            load();
          }}
        />
      )}
      {join && <JoinDialog {...join} onClose={() => setJoin(null)} />}
    </Card>
  );
}

function AddServerDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (name: string, command: string) => void }) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res = await api.createServer(name);
      onCreated(res.server.name, res.command);
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
            <DialogTitle>Añadir servidor</DialogTitle>
            <DialogDescription>Solo necesita Docker. Después ejecutarás un comando en él.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="server-name">Nombre</Label>
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
              Cancelar
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              Continuar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function JoinDialog({ name, command, onClose }: { name: string; command: string; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Conecta {name}</DialogTitle>
          <DialogDescription>
            Ejecuta esto en el servidor (con Docker instalado). El agente se conecta a este panel y aparecerá como
            conectado en unos segundos. El comando lleva el token del servidor: no lo compartas.
          </DialogDescription>
        </DialogHeader>
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">{command}</pre>
        <p className="text-xs text-muted-foreground">
          Usa los puertos 80 y 443 del servidor para sus apps. El panel debe ser accesible desde él: configura su URL
          pública en Integraciones.
        </p>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => navigator.clipboard.writeText(command).then(() => toast.success('Comando copiado'))}
          >
            <Copy /> Copiar
          </Button>
          <Button onClick={onClose}>Hecho</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
