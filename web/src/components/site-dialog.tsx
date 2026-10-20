import { useState } from 'react';
import { Loader2, TriangleAlert } from 'lucide-react';
import { toast } from 'sonner';

import { CheckField } from '@/components/check-field';
import { DomainsInput } from '@/components/domains-input';
import { SSLFields } from '@/components/ssl-fields';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { api, errorMessage, type Site, type SiteInput } from '@/lib/api';

interface Props {
  /** Sitio a editar, o undefined para crear uno nuevo. */
  site?: Site;
  onClose: () => void;
  onSaved: (site: Site) => void;
}

export function SiteDialog({ site, onClose, onSaved }: Props) {
  const isNew = site === undefined;
  const [form, setForm] = useState<SiteInput>(() => ({
    name: site?.name ?? '',
    domains: [...(site?.domains ?? [])],
    ssl: site?.ssl ?? false,
    force_https: site?.force_https ?? true,
    hsts: site?.hsts ?? false,
    spa: site?.spa ?? false,
  }));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof SiteInput>(key: K, value: SiteInput[K]) => setForm((f) => ({ ...f, [key]: value }));

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const saved = isNew ? await api.createSite(form) : await api.updateSite(site.id, form);
      toast.success(isNew ? 'Sitio creado' : 'Cambios guardados');
      onSaved(saved);
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
            <DialogTitle>{isNew ? 'Nuevo sitio' : `Editar ${site.name}`}</DialogTitle>
            <DialogDescription>
              {isNew ? 'Después podrás subir los archivos de tu build.' : 'Los cambios se aplican al instante.'}
            </DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="details">
            <TabsList className="w-full">
              <TabsTrigger value="details">Detalles</TabsTrigger>
              <TabsTrigger value="ssl">SSL</TabsTrigger>
            </TabsList>

            <TabsContent value="details" className="space-y-5 pt-3">
              <div className="space-y-2">
                <Label htmlFor="site-name">Nombre</Label>
                <Input
                  id="site-name"
                  value={form.name}
                  onChange={(e) => set('name', e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
                  placeholder="mi-web"
                  required
                  autoFocus={isNew}
                />
                <p className="text-xs text-muted-foreground">Minúsculas, números y guiones.</p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="site-domains">Dominios</Label>
                <DomainsInput id="site-domains" value={form.domains} onChange={(d) => set('domains', d)} />
              </div>
              <CheckField
                id="spa"
                checked={form.spa}
                onChange={(v) => set('spa', v)}
                label="Aplicación de una sola página (SPA)"
                hint="Sirve index.html en cualquier ruta que no exista. Actívalo para React Router, Vue Router, etc."
              />
            </TabsContent>

            <TabsContent value="ssl" className="pt-3">
              <SSLFields value={form} onChange={set} />
            </TabsContent>
          </Tabs>

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
              {isNew ? 'Crear sitio' : 'Guardar'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
