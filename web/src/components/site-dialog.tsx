import { useState } from 'react';
import { Loader2, TriangleAlert } from 'lucide-react';
import { Trans, useLingui } from '@lingui/react/macro';
import { toast } from 'sonner';

import { AccessSelect } from '@/components/access-select';
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
  const { t } = useLingui();
  const isNew = site === undefined;
  const [form, setForm] = useState<SiteInput>(() => ({
    name: site?.name ?? '',
    domains: [...(site?.domains ?? [])],
    ssl: site?.ssl ?? false,
    force_https: site?.force_https ?? true,
    hsts: site?.hsts ?? false,
    spa: site?.spa ?? false,
    previews: site?.previews ?? false,
    access_list_id: site?.access_list_id ?? null,
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
      toast.success(isNew ? t`Site created` : t`Changes saved`);
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
            <DialogTitle>{isNew ? <Trans>New site</Trans> : <Trans>Edit {site.name}</Trans>}</DialogTitle>
            <DialogDescription>
              {isNew ? (
                <Trans>You can upload your build files afterwards.</Trans>
              ) : (
                <Trans>Changes apply instantly.</Trans>
              )}
            </DialogDescription>
          </DialogHeader>

          <Tabs defaultValue="details">
            <TabsList className="w-full">
              <TabsTrigger value="details">
                <Trans>Details</Trans>
              </TabsTrigger>
              <TabsTrigger value="ssl">
                <Trans>SSL and access</Trans>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="details" className="space-y-5 pt-3">
              <div className="space-y-2">
                <Label htmlFor="site-name">
                  <Trans>Name</Trans>
                </Label>
                <Input
                  id="site-name"
                  value={form.name}
                  onChange={(e) => set('name', e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
                  placeholder={t`my-site`}
                  required
                  autoFocus={isNew}
                />
                <p className="text-xs text-muted-foreground">
                  <Trans>Lowercase letters, numbers and hyphens.</Trans>
                </p>
              </div>
              <div className="space-y-2">
                <Label htmlFor="site-domains">
                  <Trans>Domains</Trans>
                </Label>
                <DomainsInput id="site-domains" value={form.domains} onChange={(d) => set('domains', d)} />
              </div>
              <CheckField
                id="spa"
                checked={form.spa}
                onChange={(v) => set('spa', v)}
                label={t`Single-page application (SPA)`}
                hint={t`Serves index.html for any path that doesn't exist. Turn it on for React Router, Vue Router, etc.`}
              />
              <CheckField
                id="previews"
                checked={form.previews}
                onChange={(v) => set('previews', v)}
                label={t`Branch previews`}
                hint={t`Every push to another branch is published at its own URL (branch--site.previews-domain). Set the domain in Settings.`}
              />
            </TabsContent>

            <TabsContent value="ssl" className="space-y-6 pt-3">
              <SSLFields value={form} onChange={set} />
              <AccessSelect value={form.access_list_id} onChange={(id) => set('access_list_id', id)} />
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
              <Trans>Cancel</Trans>
            </Button>
            <Button type="submit" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              {isNew ? <Trans>Create site</Trans> : <Trans>Save</Trans>}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
