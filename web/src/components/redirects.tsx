import { useCallback, useEffect, useState } from 'react';
import { ArrowRight, Loader2, Pencil, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { CertBadge } from '@/components/cert-badge';
import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { api, errorMessage, type RedirectHost, type RedirectInput } from '@/lib/api';
import { useCertificates } from '@/lib/use-certificates';
import { cn } from '@/lib/utils';
import { useCan } from '@/lib/roles';

export function Redirects() {
  const canEdit = useCan('admin');
  const [list, setList] = useState<RedirectHost[]>([]);
  const [editing, setEditing] = useState<RedirectHost | 'new' | null>(null);
  const certs = useCertificates();
  const load = useCallback(() => {
    api.listRedirects().then(setList).catch((e) => toast.error(errorMessage(e)));
  }, []);
  useEffect(load, [load]);

  const input = (r: RedirectHost): RedirectInput => ({
    domains: r.domains, target: r.target, permanent: r.permanent, preserve_path: r.preserve_path, ssl: r.ssl, enabled: r.enabled,
  });

  async function toggle(r: RedirectHost) {
    try {
      await api.updateRedirect(r.id, { ...input(r), enabled: !r.enabled });
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  async function remove(r: RedirectHost) {
    try {
      await api.deleteRedirect(r.id);
      load();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Redirecciones</CardTitle>
        <CardDescription>Envía un dominio a otra URL: www → raíz, un dominio antiguo al nuevo…</CardDescription>
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
                <TableHead className="pl-6">Dominios</TableHead>
                <TableHead>Destino</TableHead>
                <TableHead>SSL</TableHead>
                <TableHead>Activa</TableHead>
                <TableHead className="pr-6" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((r) => (
                <TableRow key={r.id} className={cn(!r.enabled && 'opacity-55')}>
                  <TableCell className="pl-6 font-medium">{r.domains.join(', ')}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1.5">
                      <ArrowRight className="size-3.5 text-muted-foreground" />
                      <code className="font-mono text-xs">
                        {r.target}
                        {r.preserve_path && '/…'}
                      </code>
                      <Badge variant="outline" className="font-normal">
                        {r.permanent ? '301' : '302'}
                      </Badge>
                    </div>
                  </TableCell>
                  <TableCell>
                    {r.ssl ? (
                      <CertBadge domains={r.domains} certs={certs} />
                    ) : (
                      <Badge variant="outline" className="text-muted-foreground">
                        HTTP
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>
                    <Switch checked={r.enabled} onCheckedChange={() => toggle(r)} aria-label="Activa" />
                  </TableCell>
                  <TableCell className="pr-6 text-right">
                    <Button variant="ghost" size="icon-sm" onClick={() => setEditing(r)} aria-label="Editar">
                      <Pencil />
                    </Button>
                    <Button variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" onClick={() => remove(r)} aria-label="Eliminar">
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
        <RedirectDialog
          redirect={editing === 'new' ? undefined : editing}
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

function RedirectDialog({ redirect, onClose, onSaved }: { redirect?: RedirectHost; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<RedirectInput>(() => ({
    domains: redirect?.domains ?? [], target: redirect?.target ?? '', permanent: redirect?.permanent ?? true,
    preserve_path: redirect?.preserve_path ?? true, ssl: redirect?.ssl ?? true, enabled: redirect?.enabled ?? true,
  }));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof RedirectInput>(k: K, v: RedirectInput[K]) => setForm((f) => ({ ...f, [k]: v }));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (redirect) await api.updateRedirect(redirect.id, form);
      else await api.createRedirect(form);
      toast.success('Redirección guardada');
      onSaved();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const example = form.domains[0] ?? 'viejo.com';
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="contents">
          <DialogHeader>
            <DialogTitle>{redirect ? 'Editar redirección' : 'Nueva redirección'}</DialogTitle>
            <DialogDescription>Traefik responde directamente, sin ningún servidor detrás.</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="rd-domains">Dominios de origen</Label>
            <DomainsInput id="rd-domains" value={form.domains} onChange={(d) => set('domains', d)} />
          </div>
          <div className="space-y-2">
            <Label htmlFor="rd-target">Destino</Label>
            <Input id="rd-target" className="font-mono" value={form.target} onChange={(e) => set('target', e.target.value)} placeholder="https://nuevo.com" required />
          </div>
          <CheckField
            id="rd-path"
            checked={form.preserve_path}
            onChange={(v) => set('preserve_path', v)}
            label="Conservar la ruta"
            hint={`${example}/blog/post → ${form.target || 'https://nuevo.com'}/blog/post`}
          />
          <CheckField
            id="rd-perm"
            checked={form.permanent}
            onChange={(v) => set('permanent', v)}
            label="Permanente (301)"
            hint="Los buscadores y navegadores recuerdan el cambio. Desactívalo (302) si la redirección es temporal."
          />
          <CheckField
            id="rd-ssl"
            checked={form.ssl}
            onChange={(v) => set('ssl', v)}
            label="Certificado SSL para el dominio de origen"
            hint="Necesario para que https://dominio-de-origen funcione antes de redirigir."
          />
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
