import { useCallback, useContext, useEffect, useState } from 'react';
import { Copy, KeyRound, Loader2, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { api, errorMessage, type APIToken, type Role } from '@/lib/api';
import { formatDate, timeAgo } from '@/lib/format';
import { can, roleHint, roleLabel, UserContext } from '@/lib/roles';

/** Tokens de API del usuario, para el CLI y la integración continua. */
export function APITokens() {
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<string | null>(null);

  const load = useCallback(() => {
    api
      .listTokens()
      .then(setTokens)
      .catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  async function revoke(t: APIToken) {
    try {
      await api.deleteToken(t.id);
      toast.success(`Token «${t.name}» revocado`);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Tokens de API</CardTitle>
        <CardDescription>
          Para desplegar desde la terminal o la integración continua con{' '}
          <code className="font-mono">dockfly deploy</code>. Actúan en tu nombre, con el rol que elijas (nunca más que
          el tuyo).
        </CardDescription>
        <CardAction>
          <Button variant="outline" onClick={() => setCreating(true)}>
            <Plus /> Crear token
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className="px-0">
        {tokens.length === 0 ? (
          <p className="px-6 py-2 text-sm text-muted-foreground">Todavía no tienes tokens.</p>
        ) : (
          <ul className="divide-y">
            {tokens.map((t) => {
              const expired = t.expires_at !== null && new Date(t.expires_at) < new Date();
              return (
                <li key={t.id} className="flex flex-wrap items-center gap-3 px-6 py-3">
                  <KeyRound className="size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2 font-medium">
                      {t.name}
                      <code className="font-mono text-xs font-normal text-muted-foreground">{t.prefix}…</code>
                      {expired && <Badge variant="destructive">Caducado</Badge>}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      {t.last_used_at ? `Usado ${timeAgo(t.last_used_at)}` : 'Sin usar'} ·{' '}
                      {t.expires_at ? `caduca el ${formatDate(t.expires_at)}` : 'no caduca'}
                    </div>
                  </div>
                  <Badge variant="secondary">{roleLabel[t.role]}</Badge>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => revoke(t)}
                    aria-label={`Revocar el token ${t.name}`}
                  >
                    <Trash2 />
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </CardContent>
      {creating && (
        <CreateTokenDialog
          onClose={() => setCreating(false)}
          onCreated={(token) => {
            setCreating(false);
            setCreated(token);
            load();
          }}
        />
      )}
      {created && <TokenCreatedDialog token={created} onClose={() => setCreated(null)} />}
    </Card>
  );
}

function CreateTokenDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (token: string) => void }) {
  const user = useContext(UserContext);
  const roles = (['deployer', 'viewer', 'admin'] as const).filter((r) => can(user, r));
  const [name, setName] = useState('');
  const [role, setRole] = useState<Role>(roles[0] ?? 'viewer');
  const [days, setDays] = useState('90');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      onCreated((await api.createToken({ name, role, expires_days: Number(days) })).token);
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
            <DialogTitle>Crear token de API</DialogTitle>
            <DialogDescription>Dale un nombre que te diga dónde lo usas, para poder revocarlo después.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="token-name">Nombre</Label>
            <Input
              id="token-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="GitHub Actions"
              required
              autoFocus
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label>Permisos</Label>
              <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {roles.map((r) => (
                    <SelectItem key={r} value={r}>
                      {roleLabel[r]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>Caduca</Label>
              <Select value={days} onValueChange={setDays}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="30">En 30 días</SelectItem>
                  <SelectItem value="90">En 90 días</SelectItem>
                  <SelectItem value="365">En un año</SelectItem>
                  <SelectItem value="0">Nunca</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">{roleHint[role]}</p>
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
              Crear
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TokenCreatedDialog({ token, onClose }: { token: string; onClose: () => void }) {
  const example = `DOCKFLY_URL=${window.location.origin} \\
DOCKFLY_TOKEN=${token} \\
dockfly deploy ./dist --site mi-web`;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Copia tu token</DialogTitle>
          <DialogDescription>No se volverá a mostrar. Guárdalo como secreto (p. ej. en los secretos de GitHub Actions).</DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input readOnly value={token} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
          <Button variant="outline" onClick={() => navigator.clipboard.writeText(token).then(() => toast.success('Token copiado'))}>
            <Copy /> Copiar
          </Button>
        </div>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">Desplegar una carpeta</p>
          <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">{example}</pre>
          <p className="text-xs text-muted-foreground">
            El binario <code className="font-mono">dockfly</code> está en las releases del proyecto, o úsalo desde la
            imagen: <code className="font-mono">docker run --rm -v $PWD/dist:/dist -e DOCKFLY_URL -e DOCKFLY_TOKEN
            ghcr.io/&lt;propietario&gt;/dockfly deploy /dist --site mi-web</code>
          </p>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>Hecho</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
