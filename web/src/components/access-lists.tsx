import { useCallback, useEffect, useState } from 'react';
import { Loader2, Pencil, Plus, ShieldCheck, Trash2, TriangleAlert, X } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api, errorMessage, type AccessList } from '@/lib/api';
import { plural } from '@/lib/format';

export function AccessLists() {
  const [lists, setLists] = useState<AccessList[]>([]);
  const [editing, setEditing] = useState<AccessList | 'new' | null>(null);
  const load = useCallback(() => {
    api.listAccessLists().then(setLists).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function remove(l: AccessList) {
    try {
      await api.deleteAccessList(l.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Listas de acceso</CardTitle>
        <CardDescription>
          Protege proxy hosts, sitios y apps con usuario y contraseña, o permite solo ciertas IPs. Si una lista tiene
          ambas cosas, se exigen las dos.
        </CardDescription>
        <CardAction>
          <Button size="sm" onClick={() => setEditing('new')}>
            <Plus /> Nueva lista
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {lists.length === 0 ? (
          <p className="py-4 text-center text-sm text-muted-foreground">Todavía no hay listas de acceso.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {lists.map((l) => (
              <li key={l.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                <ShieldCheck className="size-4 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="font-medium">{l.name}</div>
                  <div className="mt-1 flex flex-wrap gap-1">
                    {l.users.map((u) => (
                      <Badge key={u} variant="secondary" className="font-normal">
                        👤 {u}
                      </Badge>
                    ))}
                    {l.allow_ips.map((ip) => (
                      <Badge key={ip} variant="outline" className="font-mono font-normal">
                        {ip}
                      </Badge>
                    ))}
                  </div>
                </div>
                <span className="text-xs text-muted-foreground">
                  {l.used_by ? `Protege ${plural(l.used_by, 'elemento', 'elementos')}` : 'Sin usar'}
                </span>
                <Button variant="ghost" size="icon-sm" onClick={() => setEditing(l)} aria-label="Editar">
                  <Pencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(l)}
                  aria-label="Eliminar"
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {editing && (
        <AccessListDialog
          list={editing === 'new' ? undefined : editing}
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

function AccessListDialog({ list, onClose, onSaved }: { list?: AccessList; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(list?.name ?? '');
  const [users, setUsers] = useState(list?.users.map((u) => ({ username: u, password: '', existing: true })) ?? []);
  const [ips, setIps] = useState(list?.allow_ips.join('\n') ?? '');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    const input = {
      name,
      users: users.map(({ username, password }) => ({ username, password })),
      allow_ips: ips.split(/[\s,]+/).filter(Boolean),
    };
    try {
      if (list) await api.updateAccessList(list.id, input);
      else await api.createAccessList(input);
      toast.success('Lista guardada');
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{list ? `Editar ${list.name}` : 'Nueva lista de acceso'}</DialogTitle>
            <DialogDescription>Las contraseñas se guardan con bcrypt y no se vuelven a mostrar.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="acl-name">Nombre</Label>
            <Input id="acl-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Equipo" required />
          </div>
          <div className="space-y-2">
            <Label>Usuarios</Label>
            {users.map((u, i) => (
              <div key={i} className="flex gap-2">
                <Input
                  placeholder="usuario"
                  value={u.username}
                  onChange={(e) => setUsers(users.map((x, j) => (j === i ? { ...x, username: e.target.value } : x)))}
                />
                <Input
                  type="password"
                  placeholder={u.existing ? 'sin cambios' : 'contraseña'}
                  value={u.password}
                  onChange={(e) => setUsers(users.map((x, j) => (j === i ? { ...x, password: e.target.value } : x)))}
                  autoComplete="new-password"
                />
                <Button type="button" variant="ghost" size="icon" onClick={() => setUsers(users.filter((_, j) => j !== i))}>
                  <X />
                </Button>
              </div>
            ))}
            <Button type="button" variant="outline" size="sm" onClick={() => setUsers([...users, { username: '', password: '', existing: false }])}>
              <Plus /> Añadir usuario
            </Button>
          </div>
          <div className="space-y-2">
            <Label htmlFor="acl-ips">IPs permitidas</Label>
            <textarea
              id="acl-ips"
              className="min-h-20 w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-sm outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50"
              value={ips}
              onChange={(e) => setIps(e.target.value)}
              placeholder={'203.0.113.7\n192.168.1.0/24'}
            />
            <p className="text-xs text-muted-foreground">Una por línea. Vacío = cualquier IP.</p>
          </div>
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
