import { useCallback, useEffect, useState } from 'react';
import { ArrowRight, Loader2, Pencil, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { api, errorMessage, type Stream, type StreamInput } from '@/lib/api';
import { cn } from '@/lib/utils';
import { useCan } from '@/lib/roles';

const portLine = (s: Pick<Stream, 'listen_port' | 'protocol'>) =>
  `"${s.listen_port}:${s.listen_port}${s.protocol === 'udp' ? '/udp' : ''}"`;

function restartedToast(res: { traefik_restarted: boolean }) {
  if (res.traefik_restarted) {
    toast.info('Traefik se reinició para escuchar el nuevo puerto', { description: 'Puede haber habido un corte de ~1 s.' });
  }
}

export function Streams() {
  const canEdit = useCan('admin');
  const [list, setList] = useState<Stream[]>([]);
  const [editing, setEditing] = useState<Stream | 'new' | null>(null);
  const load = useCallback(() => {
    api.listStreams().then(setList).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function toggle(s: Stream) {
    try {
      restartedToast(await api.updateStream(s.id, { ...s, enabled: !s.enabled }));
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(s: Stream) {
    try {
      restartedToast(await api.deleteStream(s.id));
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Streams TCP/UDP</CardTitle>
        <CardDescription>
          Reenvía un puerto a un servicio que no es web: bases de datos, servidores de juegos, MQTT… Añadir o quitar un
          puerto reinicia Traefik (corte de ~1 s).
        </CardDescription>
        <CardAction>
          {canEdit && (
            <Button variant="outline" onClick={() => setEditing('new')}>
              <Plus /> Añadir
            </Button>
          )}
        </CardAction>
      </CardHeader>
      {list.length > 0 && (
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-6">Puerto</TableHead>
                <TableHead>Destino</TableHead>
                <TableHead>Publicado</TableHead>
                <TableHead>Activo</TableHead>
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
                      <Badge variant="secondary" className="text-emerald-600 dark:text-emerald-400">
                        Sí
                      </Badge>
                    ) : (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Badge variant="secondary" className="gap-1 text-amber-600">
                            <TriangleAlert /> No
                          </Badge>
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs">
                          El contenedor de Dockfly no publica este puerto: añade <code>{portLine(s)}</code> a la sección
                          ports de su docker-compose.yml y ejecuta docker compose up -d.
                        </TooltipContent>
                      </Tooltip>
                    )}
                  </TableCell>
                  <TableCell>
                    <Switch checked={s.enabled} onCheckedChange={() => toggle(s)} aria-label="Activo" />
                  </TableCell>
                  <TableCell className="pr-6 text-right">
                    <Button variant="ghost" size="icon-sm" onClick={() => setEditing(s)} aria-label="Editar">
                      <Pencil />
                    </Button>
                    <Button variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" onClick={() => remove(s)} aria-label="Eliminar">
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
            load();
          }}
        />
      )}
    </Card>
  );
}

function StreamDialog({ stream, onClose, onSaved }: { stream?: Stream; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<StreamInput>(() => ({
    protocol: stream?.protocol ?? 'tcp', listen_port: stream?.listen_port ?? 0,
    forward_host: stream?.forward_host ?? '', forward_port: stream?.forward_port ?? 0, enabled: stream?.enabled ?? true,
  }));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof StreamInput>(k: K, v: StreamInput[K]) => setForm((f) => ({ ...f, [k]: v }));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const res = stream ? await api.updateStream(stream.id, form) : await api.createStream(form);
      toast.success('Stream guardado');
      restartedToast(res);
      onSaved();
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
            <DialogTitle>{stream ? 'Editar stream' : 'Nuevo stream'}</DialogTitle>
            <DialogDescription>
              El destino puede ser una app de Dockfly por su nombre (p. ej. postgres), un contenedor o una IP.
            </DialogDescription>
          </DialogHeader>
          <ToggleGroup type="single" variant="outline" spacing={0} className="w-full" value={form.protocol}
            onValueChange={(v) => v && set('protocol', v as 'tcp' | 'udp')}>
            <ToggleGroupItem value="tcp" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">TCP</ToggleGroupItem>
            <ToggleGroupItem value="udp" className="flex-1 data-[state=on]:bg-primary data-[state=on]:text-primary-foreground">UDP</ToggleGroupItem>
          </ToggleGroup>
          <div className="space-y-2">
            <Label htmlFor="st-port">Puerto de entrada</Label>
            <Input id="st-port" type="number" min={1} max={65535} value={form.listen_port || ''} onChange={(e) => set('listen_port', Number(e.target.value))} placeholder="5432" required />
          </div>
          <div className="grid grid-cols-[1fr_7rem] gap-3">
            <div className="space-y-2">
              <Label htmlFor="st-host">Destino</Label>
              <Input id="st-host" className="font-mono" value={form.forward_host} onChange={(e) => set('forward_host', e.target.value)} placeholder="postgres" required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="st-fport">Puerto</Label>
              <Input id="st-fport" type="number" min={1} max={65535} value={form.forward_port || ''} onChange={(e) => set('forward_port', Number(e.target.value))} placeholder="5432" required />
            </div>
          </div>
          {form.listen_port > 0 && (
            <p className="text-xs text-muted-foreground">
              Recuerda publicar el puerto en el contenedor de Dockfly: <code className="font-mono">{portLine(form)}</code> en
              la sección ports de su docker-compose.yml.
            </p>
          )}
          {error && (
            <Alert variant="destructive">
              <TriangleAlert />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancelar
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              Guardar
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
