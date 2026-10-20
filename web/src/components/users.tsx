import { useCallback, useContext, useEffect, useState } from 'react';
import { Copy, KeyRound, Loader2, Plus, Trash2, UserRound } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type PasswordLink, type Role, type UserEntry } from '@/lib/api';
import { formatDate } from '@/lib/format';
import { roleHint, roleLabel, UserContext } from '@/lib/roles';

/** Usuarios del panel y sus roles (solo administradores). */
export function Users() {
  const me = useContext(UserContext);
  const [users, setUsers] = useState<UserEntry[]>([]);
  const [editing, setEditing] = useState<UserEntry | 'new' | null>(null);
  const [link, setLink] = useState<(PasswordLink & { user: UserEntry; invite: boolean }) | null>(null);

  const load = useCallback(() => {
    api
      .listUsers()
      .then(setUsers)
      .catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function newLink(u: UserEntry) {
    try {
      setLink({ ...(await api.userLink(u.id)), user: u, invite: u.pending });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(u: UserEntry) {
    try {
      await api.deleteUser(u.id);
      toast.success(`${u.name} ya no tiene acceso`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Usuarios</CardTitle>
        <CardDescription>
          Invita a tu equipo con un enlace. Cada uno entra con su email y contraseña (y su propia verificación en dos
          pasos).
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={() => setEditing('new')}>
            <Plus /> Invitar
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y">
          {users.map((u) => (
            <li key={u.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
              <UserRound className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2 font-medium">
                  {u.name}
                  {u.id === me?.id && <span className="text-xs font-normal text-muted-foreground">(tú)</span>}
                  {u.pending && <Badge variant="outline">Invitación pendiente</Badge>}
                  {u.totp_enabled && <Badge variant="secondary">2FA</Badge>}
                </div>
                <div className="text-xs text-muted-foreground">
                  {u.email} · desde {formatDate(u.created_at)}
                </div>
              </div>
              <Badge variant={u.role === 'admin' ? 'default' : 'secondary'}>{roleLabel[u.role]}</Badge>
              <Button variant="ghost" size="sm" onClick={() => setEditing(u)}>
                Editar
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => newLink(u)}
                aria-label={u.pending ? `Nuevo enlace de invitación para ${u.name}` : `Restablecer la contraseña de ${u.name}`}
                title={u.pending ? 'Nuevo enlace de invitación' : 'Enlace para restablecer la contraseña'}
              >
                <KeyRound />
              </Button>
              {u.id !== me?.id && (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-destructive hover:text-destructive"
                  onClick={() => remove(u)}
                  aria-label={`Quitar el acceso a ${u.name}`}
                >
                  <Trash2 />
                </Button>
              )}
            </li>
          ))}
        </ul>
      </CardContent>
      {editing && (
        <UserDialog
          user={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={(res) => {
            setEditing(null);
            if (res) setLink({ ...res, invite: true });
            load();
          }}
        />
      )}
      {link && <LinkDialog link={link} onClose={() => setLink(null)} />}
    </Card>
  );
}

function UserDialog({
  user,
  onClose,
  onSaved,
}: {
  user?: UserEntry;
  onClose: () => void;
  onSaved: (invite?: PasswordLink & { user: UserEntry }) => void;
}) {
  const [name, setName] = useState(user?.name ?? '');
  const [email, setEmail] = useState(user?.email ?? '');
  const [role, setRole] = useState<Role>(user?.role ?? 'deployer');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (user) {
        await api.updateUser(user.id, { name, role });
        toast.success('Cambios guardados');
        onSaved();
      } else {
        onSaved(await api.inviteUser({ name, email, role }));
      }
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
            <DialogTitle>{user ? `Editar a ${user.name}` : 'Invitar a alguien'}</DialogTitle>
            <DialogDescription>
              {user ? 'El cambio de rol se aplica al momento.' : 'Recibirás un enlace para enviárselo como prefieras.'}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="user-name">Nombre</Label>
            <Input id="user-name" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
          </div>
          {!user && (
            <div className="space-y-2">
              <Label htmlFor="user-email">Email</Label>
              <Input id="user-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
            </div>
          )}
          <div className="space-y-2">
            <Label>Rol</Label>
            <Select value={role} onValueChange={(v) => setRole(v as Role)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(['admin', 'deployer', 'viewer'] as const).map((r) => (
                  <SelectItem key={r} value={r}>
                    {roleLabel[r]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">{roleHint[role]}</p>
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
              {user ? 'Guardar' : 'Crear invitación'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function LinkDialog({
  link,
  onClose,
}: {
  link: PasswordLink & { user: UserEntry; invite: boolean };
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{link.invite ? `Invitación para ${link.user.name}` : `Restablecer la contraseña de ${link.user.name}`}</DialogTitle>
          <DialogDescription>
            Envíale este enlace. Con él elegirá su contraseña; sirve una sola vez y caduca el {formatDate(link.expires_at)}.
            {!link.invite && ' Su contraseña actual sigue valiendo hasta que lo use.'}
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input readOnly value={link.link} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
          <Button
            variant="outline"
            onClick={() => navigator.clipboard.writeText(link.link).then(() => toast.success('Enlace copiado'))}
          >
            <Copy /> Copiar
          </Button>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Hecho</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
